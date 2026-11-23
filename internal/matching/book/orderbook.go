// Package book implements the mutable order book owned by a single matching actor.
package book

import (
	"container/list"
	"errors"
	"fmt"
	"math"
)

type Side uint8

const (
	Buy Side = iota + 1
	Sell
)

type Kind uint8

const (
	Limit Kind = iota + 1
	Market
	IOC
	FOK
)

type Reject uint8

const (
	RejectNone Reject = iota
	RejectFOKUnfilled
	RejectNoLiquidity
)

const maxFillsPerSubmit = 512 // Keeps the unary response and settlement transaction bounded.

var (
	ErrLevelOverflow = errors.New("price level quantity exceeds int64 ticks")
	errFillLimit     = errors.New("submit would exceed the fill limit")
)

// Order is the validated input to a single book. Prices and quantities are in
// 1e-8 ticks. Market orders ignore Price.
type Order struct {
	ID         string
	UserID     int64
	Side       Side
	Kind       Kind
	Price, Qty int64
}

type Fill struct {
	MakerID     string
	MakerUserID int64
	Price, Qty  int64
}

type Result struct {
	Fills             []Fill
	Filled, Remaining int64
	Resting           bool
	Reject            Reject
}

type Resting struct {
	ID        string
	UserID    int64
	Side      Side
	Price     int64
	Remaining int64
}

type Level struct {
	Price, Qty int64
	Orders     int
}

type level struct {
	total  int64
	orders list.List
}

type restingOrder struct {
	Resting
	level *level
}

type side struct {
	bids   bool
	prices *SkipList[int64]
	levels map[int64]*level
}

func newSide(bids bool) side {
	return side{
		bids:   bids,
		prices: NewSkipList(func(a, b int64) bool { return a < b }),
		levels: make(map[int64]*level),
	}
}

func (s *side) best() *SkipListNode[int64] {
	if s.bids {
		return s.prices.SeekLast()
	}
	return s.prices.SeekFirst()
}

func (s *side) next(n *SkipListNode[int64]) *SkipListNode[int64] {
	if s.bids {
		return n.Prev()
	}
	return n.Next()
}

func (s *side) remove(price int64) {
	delete(s.levels, price)
	s.prices.Delete(price)
}

// Book has no synchronization. Its owner serializes all reads and writes.
type Book struct {
	bids, asks side
	index      map[string]*list.Element
}

func New() *Book {
	return &Book{bids: newSide(true), asks: newSide(false), index: make(map[string]*list.Element)}
}

func (b *Book) sides(s Side) (own, opposite *side) {
	if s == Buy {
		return &b.bids, &b.asks
	}
	return &b.asks, &b.bids
}

func (o Order) crosses(price int64) bool {
	return o.Kind == Market || (o.Side == Buy && o.Price >= price) || (o.Side == Sell && o.Price <= price)
}

// unfilled estimates the remainder without mutation. Subtracting the available
// volume from the request avoids summing quantities from multiple levels.
func unfilled(o Order, opposite *side) int64 {
	remaining := o.Qty
	for n := opposite.best(); n != nil && o.crosses(n.Key); n = opposite.next(n) {
		qty := opposite.levels[n.Key].total
		if qty >= remaining {
			return 0
		}
		remaining -= qty
	}
	return remaining
}

// CheckSubmit verifies both capacity limits before an actor persists an order.
// Full levels can be checked by their count and aggregate volume. Only a final
// partial level may require looking at individual makers.
func (b *Book) CheckSubmit(o Order) error {
	own, opposite := b.sides(o.Side)
	remaining, budget := o.Qty, maxFillsPerSubmit
	for n := opposite.best(); n != nil && remaining > 0 && o.crosses(n.Key); n = opposite.next(n) {
		l := opposite.levels[n.Key]
		if remaining >= l.total {
			if l.orders.Len() > budget {
				return errFillLimit
			}
			remaining -= l.total
			budget -= l.orders.Len()
			continue
		}
		if l.orders.Len() <= budget {
			remaining = 0
			break
		}
		for e := l.orders.Front(); remaining > 0; e = e.Next() {
			if budget == 0 {
				return errFillLimit
			}
			remaining -= min(remaining, e.Value.(*restingOrder).Remaining)
			budget--
		}
		break
	}
	if l := own.levels[o.Price]; o.Kind == Limit && l != nil && remaining > math.MaxInt64-l.total {
		return ErrLevelOverflow
	}
	return nil
}

// Submit applies a validated and checked order that is absent from the book.
func (b *Book) Submit(o Order) Result {
	own, opposite := b.sides(o.Side)
	r := Result{Remaining: o.Qty}
	if o.Kind == FOK && unfilled(o, opposite) != 0 {
		r.Reject = RejectFOKUnfilled
		return r
	}

	for n := opposite.best(); n != nil && r.Remaining > 0 && o.crosses(n.Key); {
		price := n.Key
		n = opposite.next(n)
		l := opposite.levels[price]
		for r.Remaining > 0 && l.orders.Len() > 0 {
			head := l.orders.Front()
			maker := head.Value.(*restingOrder)
			qty := min(r.Remaining, maker.Remaining)
			r.Fills = append(r.Fills, Fill{MakerID: maker.ID, MakerUserID: maker.UserID, Price: price, Qty: qty})
			r.Remaining -= qty
			maker.Remaining -= qty
			l.total -= qty
			if maker.Remaining == 0 {
				delete(b.index, maker.ID)
				l.orders.Remove(head)
			}
		}
		if l.orders.Len() == 0 {
			opposite.remove(price)
		}
	}

	r.Filled = o.Qty - r.Remaining
	if o.Kind == Market && r.Filled == 0 {
		r.Reject = RejectNoLiquidity
	}
	if o.Kind == Limit && r.Remaining != 0 {
		if err := b.add(own, Resting{ID: o.ID, UserID: o.UserID, Side: o.Side, Price: o.Price, Remaining: r.Remaining}); err != nil {
			panic(err) // CheckSubmit must run before the actor persists this order.
		}
		r.Resting = true
	}
	return r
}

func (b *Book) add(s *side, o Resting) error {
	l := s.levels[o.Price]
	if l == nil {
		l = &level{}
		s.levels[o.Price] = l
		s.prices.Insert(o.Price)
	}
	if o.Remaining > math.MaxInt64-l.total {
		return ErrLevelOverflow
	}
	l.total += o.Remaining
	b.index[o.ID] = l.orders.PushBack(&restingOrder{Resting: o, level: l})
	return nil
}

func (b *Book) Cancel(id string) (remaining int64, ok bool) {
	e, ok := b.index[id]
	if !ok {
		return 0, false
	}
	o := e.Value.(*restingOrder)
	delete(b.index, id)
	o.level.total -= o.Remaining
	o.level.orders.Remove(e)
	if o.level.orders.Len() == 0 {
		s, _ := b.sides(o.Side)
		s.remove(o.Price)
	}
	return o.Remaining, true
}

func (s *side) depth(n int) []Level {
	levels := make([]Level, 0, min(n, len(s.levels)))
	for p := s.best(); p != nil && len(levels) < n; p = s.next(p) {
		l := s.levels[p.Key]
		levels = append(levels, Level{Price: p.Key, Qty: l.total, Orders: l.orders.Len()})
	}
	return levels
}

// Depth returns up to n aggregated levels per side, ordered best price first.
func (b *Book) Depth(n int) (bids, asks []Level) {
	if n <= 0 {
		return nil, nil
	}
	return b.bids.depth(n), b.asks.depth(n)
}

// Resting returns the complete book, bids first, in price-time order.
func (b *Book) Resting() []Resting {
	orders := make([]Resting, 0, len(b.index))
	for _, s := range [2]*side{&b.bids, &b.asks} {
		for n := s.best(); n != nil; n = s.next(n) {
			for e := s.levels[n.Key].orders.Front(); e != nil; e = e.Next() {
				orders = append(orders, e.Value.(*restingOrder).Resting)
			}
		}
	}
	return orders
}

// Restore installs a complete book on an empty book. Orders at a given price
// are restored in slice order. On error the book remains empty.
func (b *Book) Restore(orders []Resting) error {
	if b.Len() != 0 {
		return errors.New("cannot restore into a non-empty book")
	}
	restored := New()
	for _, o := range orders {
		if o.ID == "" || o.UserID <= 0 || (o.Side != Buy && o.Side != Sell) || o.Price <= 0 || o.Remaining <= 0 {
			return fmt.Errorf("invalid resting order %q", o.ID)
		}
		if restored.Has(o.ID) {
			return fmt.Errorf("duplicate resting order %q", o.ID)
		}
		s, _ := restored.sides(o.Side)
		if err := restored.add(s, o); err != nil {
			return err
		}
	}
	*b = *restored
	return nil
}

func (b *Book) Has(id string) bool { return b.index[id] != nil }
func (b *Book) Len() int           { return len(b.index) }
