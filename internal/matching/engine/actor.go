package engine

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/shopspring/decimal"
)

type command struct {
	id        int64
	orderID   string
	userID    int64
	side      model.OrderSide
	orderType model.OrderType
	price     decimal.Decimal
	qty       decimal.Decimal
	replyCh   chan<- *MatchResult
}

type cancelCommand struct {
	orderID string
	replyCh chan<- bool
}

type actor struct {
	symbol  string
	cmdCh   chan command
	cancelCh chan cancelCommand
	book    *book.OrderBook
	cancel  context.CancelFunc
	exited  atomic.Bool
}

func (a *actor) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-a.cmdCh:
			if !ok {
				return
			}
			a.handleCommand(cmd)
		case cmd, ok := <-a.cancelCh:
			if !ok {
				return
			}
			a.handleCancelCommand(cmd)
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
		cmd.orderType,
		cmd.price,
		cmd.qty,
	)

	trades, matchErr := a.book.AddOrder(order)

	result := &MatchResult{
		OrderID:     cmd.orderID,
		Trades:      trades,
		Remaining:   order.RemainingQty,
		UnfilledQty: order.UnfilledQty,
		Status:      string(order.Status),
		Timestamp:   time.Now(),
		Error:       matchErr,
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

func (a *actor) handleCancelCommand(cmd cancelCommand) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("actor recovered from cancel panic",
				logger.S("symbol", a.symbol),
				logger.Any("panic", r),
			)
		}
	}()

	success := a.book.CancelOrder(cmd.orderID)
	select {
	case cmd.replyCh <- success:
	default:
		logger.Warn("cancel reply channel full",
			logger.S("symbol", a.symbol),
			logger.S("order_id", cmd.orderID),
		)
	}
}
