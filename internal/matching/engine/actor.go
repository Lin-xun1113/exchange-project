package engine

import (
	"context"
	"sync"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/shopspring/decimal"
)

type command struct {
	id     int64
	orderID string
	userID  int64
	side    model.OrderSide
	price   decimal.Decimal
	qty     decimal.Decimal
	replyCh chan<- *MatchResult
}

type actor struct {
	symbol string
	cmdCh  chan command
	book   *book.OrderBook
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

func (a *actor) run(ctx context.Context) {
	defer func() {
		close(a.cmdCh)
		a.wg.Done()
	}()

	for cmd := range a.cmdCh {
		select {
		case <-ctx.Done():
			return
		default:
			a.handleCommand(cmd)
		}
	}
}

func (a *actor) handleCommand(cmd command) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("actor recovered from panic",
				logger.S("symbol", a.symbol),
				logger.Any("panic", r),
			)
		}
	}()

	order := book.NewOrderInBook(
		cmd.id,
		cmd.orderID,
		cmd.userID,
		a.symbol,
		cmd.side,
		cmd.price,
		cmd.qty,
	)

	trades := a.book.AddOrder(order)

	result := &MatchResult{
		OrderID:   cmd.orderID,
		Trades:    trades,
		Remaining: order.RemainingQty,
		Status:    string(order.Status),
		Timestamp: time.Now(),
	}

	select {
	case cmd.replyCh <- result:
	default:
		logger.Warn("reply channel full",
			logger.S("symbol", a.symbol),
			logger.S("order_id", cmd.orderID),
		)
	}
}
