package snapshot

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/shopspring/decimal"
)

// OrderState represents a serializable snapshot of a single order.
type OrderState struct {
	ID             int64
	OrderID        string
	UserID         int64
	Symbol         string
	Side           model.OrderSide
	OrderType      model.OrderType
	Price          string
	Quantity       string
	FilledQuantity string
	RemainingQty   string
	Status         model.OrderStatus
	CreatedAt      int64
}

// Snapshot represents a point-in-time dump of the order book.
type Snapshot struct {
	Symbol    string
	EndingLSN uint64
	Bids      []OrderState
	Asks      []OrderState
}

// Take captures the current state of an order book into a Snapshot.
func Take(symbol string, ob *book.OrderBook, walLSN uint64) *Snapshot {
	bids, asks := ob.GetDepth(0)

	snap := &Snapshot{
		Symbol:    symbol,
		EndingLSN: walLSN,
		Bids:      make([]OrderState, 0, len(bids)),
		Asks:      make([]OrderState, 0, len(asks)),
	}

	for _, b := range bids {
		snap.Bids = append(snap.Bids, orderToState(b))
	}
	for _, a := range asks {
		snap.Asks = append(snap.Asks, orderToState(a))
	}

	return snap
}

func orderToState(o *book.OrderInBook) OrderState {
	return OrderState{
		ID:             o.ID,
		OrderID:        o.OrderID,
		UserID:         o.UserID,
		Symbol:         o.Symbol,
		Side:           o.Side,
		OrderType:      o.OrderType,
		Price:          o.Price.String(),
		Quantity:       o.Quantity.String(),
		FilledQuantity: o.FilledQuantity.String(),
		RemainingQty:   o.RemainingQty.String(),
		Status:         o.Status,
		CreatedAt:      o.CreatedAt,
	}
}

func stateToOrder(s OrderState) (*book.OrderInBook, error) {
	price, err := decimal.NewFromString(s.Price)
	if err != nil {
		return nil, fmt.Errorf("failed to parse price: %w", err)
	}
	qty, err := decimal.NewFromString(s.Quantity)
	if err != nil {
		return nil, fmt.Errorf("failed to parse quantity: %w", err)
	}
	filled, err := decimal.NewFromString(s.FilledQuantity)
	if err != nil {
		return nil, fmt.Errorf("failed to parse filled quantity: %w", err)
	}
	remaining, err := decimal.NewFromString(s.RemainingQty)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remaining quantity: %w", err)
	}

	o := book.NewOrderInBook(
		s.ID, s.OrderID, s.UserID, s.Symbol,
		s.Side, s.OrderType, price, qty,
	)
	o.FilledQuantity = filled
	o.RemainingQty = remaining
	o.Status = s.Status
	o.CreatedAt = s.CreatedAt
	return o, nil
}

// Save writes a snapshot to disk atomically.
func Save(snap *Snapshot, dir string) error {
	// Sanitize symbol for use as filename
	safeSymbol := wal.SanitizeSymbol(snap.Symbol)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create snapshot dir %s: %w", dir, err)
	}

	filename := fmt.Sprintf("%s-%d.snap", safeSymbol, snap.EndingLSN)
	tmpPath := filepath.Join(dir, filename+".tmp")
	finalPath := filepath.Join(dir, filename)

	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp snapshot file: %w", err)
	}

	enc := gob.NewEncoder(file)
	if err := enc.Encode(snap); err != nil {
		file.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to encode snapshot: %w", err)
	}

	// Sync file content to disk before closing
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync snapshot file: %w", err)
	}

	if err := file.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close snapshot file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename snapshot file: %w", err)
	}

	// Sync parent directory to ensure rename is committed
	parentDir := dir
	if dirFile, err := os.Open(parentDir); err == nil {
		if err := dirFile.Sync(); err != nil {
			logger.Warn("failed to sync snapshot parent directory",
				logger.S("symbol", snap.Symbol),
				logger.Err(err),
			)
		}
		dirFile.Close()
	} else {
		logger.Warn("failed to open snapshot parent directory for sync",
			logger.S("symbol", snap.Symbol),
			logger.Err(err),
		)
	}

	// Update the .latest symlink.
	linkPath := filepath.Join(dir, safeSymbol+".latest")
	absTarget, _ := filepath.Abs(finalPath)
	os.Remove(linkPath)
	if err := os.Symlink(absTarget, linkPath); err != nil {
		logger.Warn("failed to create latest symlink",
			logger.S("symbol", snap.Symbol),
			logger.Err(err),
		)
	}

	logger.Info("snapshot saved",
		logger.S("symbol", snap.Symbol),
		logger.I64("ending_lsn", int64(snap.EndingLSN)),
		logger.S("path", finalPath),
	)

	return nil
}

// Load reads a snapshot from disk.
func Load(symbol, dir string) (*Snapshot, error) {
	// Sanitize symbol for use as filename
	safeSymbol := wal.SanitizeSymbol(symbol)

	linkPath := filepath.Join(dir, safeSymbol+".latest")

	target, err := os.Readlink(linkPath)
	if err != nil {
		return nil, fmt.Errorf("no snapshot found for %s: %w", symbol, err)
	}

	file, err := os.Open(target)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot file: %w", err)
	}
	defer file.Close()

	var snap Snapshot
	dec := gob.NewDecoder(file)
	if err := dec.Decode(&snap); err != nil {
		return nil, fmt.Errorf("failed to decode snapshot: %w", err)
	}

	logger.Info("snapshot loaded",
		logger.S("symbol", snap.Symbol),
		logger.I64("ending_lsn", int64(snap.EndingLSN)),
	)

	return &snap, nil
}

// Restore re-inserts orders from a snapshot into an order book.
// Uses batch recovery for O(n log n) performance instead of O(n²).
func Restore(snap *Snapshot, ob *book.OrderBook) error {
	orders := make([]*book.OrderInBook, 0, len(snap.Bids)+len(snap.Asks))

	for _, s := range snap.Bids {
		o, err := stateToOrder(s)
		if err != nil {
			logger.Warn("failed to restore bid order",
				logger.S("order_id", s.OrderID),
				logger.Err(err),
			)
			continue
		}
		orders = append(orders, o)
	}

	for _, s := range snap.Asks {
		o, err := stateToOrder(s)
		if err != nil {
			logger.Warn("failed to restore ask order",
				logger.S("order_id", s.OrderID),
				logger.Err(err),
			)
			continue
		}
		orders = append(orders, o)
	}

	ob.BatchAddOrdersForRecovery(orders)
	return nil
}
