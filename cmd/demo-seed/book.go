package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	bookBuy      = orderpb.OrderSide_ORDER_SIDE_BUY
	bookSell     = orderpb.OrderSide_ORDER_SIDE_SELL
	bookLimit    = orderpb.OrderType_ORDER_TYPE_LIMIT
	bookIOC      = orderpb.OrderType_ORDER_TYPE_IOC
	bookLevels   = 25
	pollInterval = 200 * time.Millisecond
	phaseTimeout = 30 * time.Second
)

type bookSeeder struct {
	orders   orderpb.OrderServiceClient
	users    userpb.UserServiceClient
	accounts *accounts
	db       *gorm.DB
	rng      *rand.Rand
	sequence [marketCount]int
}

type bookOrder struct {
	market          int
	side            orderpb.OrderSide
	kind            orderpb.OrderType
	price, quantity decimal.Decimal
	owner           *seedAccount // nil selects a trader
}

func levelPrice(last decimal.Decimal, side orderpb.OrderSide, level int) decimal.Decimal {
	factor := int64(100000)
	offset := int64(6 + 12*level)
	if side == bookBuy {
		factor -= offset
	} else {
		factor += offset
	}
	return last.Mul(decimal.New(factor, -5)).Round(2)
}

func (s *bookSeeder) seed(ctx context.Context, last [marketCount]decimal.Decimal) error {
	for i, price := range last {
		if !price.IsPositive() {
			return fmt.Errorf("missing historical price for %s", seedMarkets[i].symbol)
		}
	}
	if err := s.resting(ctx, last); err != nil {
		return fmt.Errorf("resting book: %w", err)
	}
	if err := s.prints(ctx, last); err != nil {
		return fmt.Errorf("live prints: %w", err)
	}
	demoAsk := levelPrice(last[btcMarket], bookSell, 0).Sub(decimal.New(50, -2))
	if !demoAsk.GreaterThan(levelPrice(last[btcMarket], bookBuy, 0)) {
		return fmt.Errorf("demo ask %s would cross the BTC book", demoAsk)
	}
	demo := &s.accounts[demoAccount]
	phase := make([]orderExpectation, 0, 3)
	for _, order := range [...]bookOrder{
		{btcMarket, bookBuy, bookLimit, levelPrice(last[btcMarket], bookBuy, 3), decimal.New(5, -2), demo},
		{btcMarket, bookSell, bookLimit, demoAsk, decimal.New(4, -2), demo},
		{ethMarket, bookBuy, bookLimit, levelPrice(last[ethMarket], bookBuy, 2), decimal.New(8, -1), demo},
	} {
		if err := s.submit(ctx, &phase, order); err != nil {
			return fmt.Errorf("demo orders: %w", err)
		}
	}
	// Waiting compacts phase in place; retain the maker's identity first.
	maker := phase[1]
	if err := s.wait(ctx, phase); err != nil {
		return fmt.Errorf("demo orders: %w", err)
	}

	maker.status = orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED
	maker.filled = decimal.New(15, -3)
	phase = append(phase[:0], maker)
	if err := s.submit(ctx, &phase, bookOrder{btcMarket, bookBuy, bookIOC, demoAsk, maker.filled, nil}); err != nil {
		return fmt.Errorf("demo partial fill: %w", err)
	}
	if err := s.wait(ctx, phase); err != nil {
		return fmt.Errorf("demo partial fill: %w", err)
	}
	return s.refreshBalances(ctx)
}

func (s *bookSeeder) resting(ctx context.Context, last [marketCount]decimal.Decimal) error {
	phase := make([]orderExpectation, 0, marketCount*2*(bookLevels+5))
	for market, price := range last {
		for i := range bookLevels {
			for _, side := range [...]orderpb.OrderSide{bookBuy, bookSell} {
				makerQty := 0.08 + 0.05*float64(i) + 0.25*s.rng.Float64()
				var traderMin, traderRange float64 = 0.002, 0.048
				if market == ethMarket {
					makerQty = 1.5 + 0.9*float64(i) + 4*s.rng.Float64()
					traderMin, traderRange = 0.05, 1.15
				}
				dp := seedMarkets[market].qtyDP
				order := bookOrder{market, side, bookLimit, levelPrice(price, side, i), decimal.NewFromFloat(makerQty).Round(dp), &s.accounts[i%2]}
				if err := s.submit(ctx, &phase, order); err != nil {
					return err
				}
				switch i {
				case 0, 1, 2, 4, 7:
					order.owner = nil
					order.quantity = decimal.NewFromFloat(traderMin + traderRange*s.rng.Float64()).Round(dp)
					if err := s.submit(ctx, &phase, order); err != nil {
						return err
					}
				}
			}
		}
	}
	return s.wait(ctx, phase)
}

func (s *bookSeeder) prints(ctx context.Context, last [marketCount]decimal.Decimal) error {
	const printsPerMarket = 6
	phase := make([]orderExpectation, 0, 1)
	for market, price := range last {
		bid, ask := levelPrice(price, bookBuy, 0), levelPrice(price, bookSell, 0)
		qtyMin, qtyRange := 0.004, 0.016
		if market == ethMarket {
			qtyMin, qtyRange = 0.05, 0.35
		}
		for i := range printsPerMarket {
			side, limit := bookBuy, ask
			if i%2 != 0 {
				side, limit = bookSell, bid
			}
			qty := decimal.NewFromFloat(qtyMin + qtyRange*s.rng.Float64()).Round(seedMarkets[market].qtyDP)
			if err := s.submit(ctx, &phase, bookOrder{market, side, bookIOC, limit, qty, nil}); err != nil {
				return err
			}
			if err := s.wait(ctx, phase); err != nil {
				return err
			}
			phase = phase[:0]
		}
	}
	return s.refreshBalances(ctx)
}

func (s *bookSeeder) submit(ctx context.Context, phase *[]orderExpectation, order bookOrder) error {
	market := seedMarkets[order.market]
	asset, amount := market.base, order.quantity
	if order.side == bookBuy {
		asset, amount = quoteAsset, order.price.Mul(order.quantity)
	}
	owner := order.owner
	if owner == nil {
		var funded [traderCount]*seedAccount
		n := 0
		for i := firstTrader; i < demoAccount; i++ {
			a := &s.accounts[i]
			if a.canSpend(asset, amount, decimal.Zero) {
				funded[n] = a
				n++
			}
		}
		if n == 0 {
			if order.kind == bookLimit {
				return nil // Optional trader liquidity.
			}
			return fmt.Errorf("no trader can fund %s %s on %s", amount, asset, market.symbol)
		}
		owner = funded[s.rng.IntN(n)]
	}
	if !owner.canSpend(asset, amount, decimal.Zero) {
		return fmt.Errorf("%s cannot fund %s %s on %s", owner.name, amount, asset, market.symbol)
	}
	s.sequence[order.market]++
	key := fmt.Sprintf("seed-book-%s-%d", market.code, s.sequence[order.market])
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	resp, err := s.orders.CreateOrder(rpcCtx, &orderpb.CreateOrderRequest{
		UserId: owner.id, Symbol: market.symbol, Side: order.side, Type: order.kind,
		Price: order.price.String(), Quantity: order.quantity.String(), IdempotencyKey: key,
	})
	if err != nil {
		return fmt.Errorf("submit %s for %s: %w", key, owner.name, err)
	}
	id := resp.GetOrder().GetOrderId()
	if id == "" {
		return fmt.Errorf("submit %s: missing order ID", key)
	}
	owner.available[asset] = owner.available[asset].Sub(amount)
	want := orderExpectation{id: id, user: owner.id, status: orderpb.OrderStatus_ORDER_STATUS_OPEN}
	if order.kind == bookIOC {
		want.status = orderpb.OrderStatus_ORDER_STATUS_FILLED
		want.filled = order.quantity
	}
	*phase = append(*phase, want)
	return nil
}

type orderExpectation struct {
	id     string
	user   int64
	status orderpb.OrderStatus
	filled decimal.Decimal
}

func (want orderExpectation) ready(order *orderpb.Order) (bool, error) {
	if order.GetOrderId() != want.id {
		return false, fmt.Errorf("GetOrder %s: missing or mismatched order", want.id)
	}
	status := order.GetStatus()
	if status == orderpb.OrderStatus_ORDER_STATUS_PENDING ||
		(status == orderpb.OrderStatus_ORDER_STATUS_OPEN && want.status == orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED) {
		return false, nil
	}
	if status != want.status {
		return false, fmt.Errorf("order %s in %s, expected %s (reason %q)", want.id, status, want.status, order.GetRejectReason())
	}
	filled, err := decimal.NewFromString(order.GetFilledQuantity())
	if err != nil || !filled.Equal(want.filled) {
		return false, fmt.Errorf("order %s filled %q, expected %s", want.id, order.GetFilledQuantity(), want.filled)
	}
	return true, nil
}

func (s *bookSeeder) wait(ctx context.Context, phase []orderExpectation) error {
	phaseCtx, cancel := context.WithTimeout(ctx, phaseTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// The submit transaction updates fills before the ledger worker settles them.
	// Wait for the IOC outbox entries before refreshing the credited balances.
	var iocIDs []string
	for _, want := range phase {
		if want.status == orderpb.OrderStatus_ORDER_STATUS_FILLED {
			iocIDs = append(iocIDs, want.id)
		}
	}
	for phaseCtx.Err() == nil {
		remaining, err := s.pollOrders(phaseCtx, phase)
		if phaseCtx.Err() != nil {
			break
		}
		if err != nil {
			return err
		}
		phase = phase[:remaining]
		if len(phase) == 0 {
			if len(iocIDs) == 0 {
				return nil
			}
			var incomplete struct {
				AggregateID string
				Step        string
				Status      string
			}
			res := s.db.WithContext(phaseCtx).Table("outbox").Select("aggregate_id, step, status").
				Where("aggregate_id IN ? AND status IN ('dead', 'pending')", iocIDs).
				Order("status DESC").Limit(1).Find(&incomplete)
			if phaseCtx.Err() != nil {
				break
			}
			if res.Error != nil {
				return fmt.Errorf("check live settlement: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				return nil
			}
			if incomplete.Status == "dead" {
				return fmt.Errorf("outbox %s %s failed", incomplete.AggregateID, incomplete.Step)
			}
		}
		select {
		case <-phaseCtx.Done():
		case <-ticker.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(phase) == 0 {
		return fmt.Errorf("live settlement did not complete within %s", phaseTimeout)
	}
	return fmt.Errorf("orders stuck in pending: %s waiting for %s", phase[0].id, phase[0].status)
}

func (s *bookSeeder) pollOrders(ctx context.Context, phase []orderExpectation) (int, error) {
	remaining := 0
	for _, want := range phase {
		rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		resp, err := s.orders.GetOrder(rpcCtx, &orderpb.GetOrderRequest{UserId: want.user, OrderId: want.id})
		cancel()
		if err != nil {
			return 0, fmt.Errorf("get order %s: %w", want.id, err)
		}
		ready, err := want.ready(resp.GetOrder())
		if err != nil {
			return 0, err
		}
		if !ready {
			phase[remaining] = want
			remaining++
		}
	}
	return remaining, nil
}

func (s *bookSeeder) refreshBalances(ctx context.Context) error {
	for i := range s.accounts {
		a := &s.accounts[i]
		rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		resp, err := s.users.GetBalances(rpcCtx, &userpb.GetBalancesRequest{UserId: a.id})
		cancel()
		if err != nil {
			return fmt.Errorf("get balances for %s: %w", a.name, err)
		}
		for _, balance := range resp.GetBalances() {
			if _, tracked := a.available[balance.Asset]; !tracked {
				continue
			}
			available, err := decimal.NewFromString(balance.Available)
			if err != nil {
				return fmt.Errorf("decode %s %s balance: %w", a.name, balance.Asset, err)
			}
			a.available[balance.Asset] = available
		}
	}
	return nil
}
