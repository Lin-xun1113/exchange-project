package workflow

import (
	"context"
	"encoding/json"
	"fmt"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type steps struct {
	db       *gorm.DB
	user     userpb.UserServiceClient
	matching matchingpb.MatchingServiceClient
}

func Handlers(db *gorm.DB, user userpb.UserServiceClient, matching matchingpb.MatchingServiceClient) map[string]Handler {
	s := steps{db: db, user: user, matching: matching}
	return map[string]Handler{
		FreezeStep: s.freeze, SubmitStep: s.submit, CancelStep: s.cancel, ReleaseStep: s.release, settleStep: s.settle,
	}
}

func (s steps) freeze(ctx context.Context, e Entry) (func(*gorm.DB) error, error) {
	o, err := s.order(ctx, e)
	if err != nil {
		return nil, err
	}
	resp, err := s.user.Freeze(ctx, &userpb.FreezeRequest{
		UserId: o.UserID, Asset: o.FrozenAsset, Amount: o.FrozenAmount.String(), OrderId: o.OrderID,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || (!resp.Ok && resp.Reason != "insufficient_balance") || (resp.Ok && resp.Reason != "") {
		return nil, status.Error(codes.FailedPrecondition, "invalid freeze response")
	}
	if resp.Ok {
		return func(tx *gorm.DB) error { return Enqueue(tx, o.OrderID, SubmitStep, nil) }, nil
	}
	return func(tx *gorm.DB) error {
		current, err := repository.GetOrderForUpdate(tx, o.OrderID)
		if err != nil {
			return err
		}
		if !current.FilledQuantity.IsZero() || !current.ClosedQuantity.IsZero() {
			return status.Error(codes.FailedPrecondition, "freeze rejection contradicts order quantities")
		}
		current.RejectReason = "insufficient_balance"
		current.ClosedQuantity = current.Quantity
		return saveOrder(tx, &current)
	}, nil
}

func (s steps) submit(ctx context.Context, e Entry) (func(*gorm.DB) error, error) {
	o, err := s.order(ctx, e)
	if err != nil {
		return nil, err
	}
	cmd := &matchingpb.SubmitOrderRequest{
		OrderId: o.OrderID, UserId: o.UserID, Symbol: o.Symbol,
		Side: matchingSide(o.Side), Type: matchingType(o.Type), Price: o.Price.String(), Quantity: o.Quantity.String(),
	}
	response, err := s.matching.SubmitOrder(ctx, cmd)
	if err != nil {
		return nil, err
	}
	result, err := validateSubmit(o, response)
	if err != nil {
		return nil, err
	}
	return func(tx *gorm.DB) error { return result.apply(tx, o.OrderID) }, nil
}

func (s steps) cancel(ctx context.Context, e Entry) (func(*gorm.DB) error, error) {
	o, err := s.order(ctx, e)
	if err != nil {
		return nil, err
	}
	resp, err := s.matching.CancelOrder(ctx, &matchingpb.CancelOrderRequest{OrderId: o.OrderID, Symbol: o.Symbol})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, invalidMatch("missing cancel response")
	}
	if !resp.Found && resp.RemainingQuantity == "" {
		return nil, nil
	}
	remaining, ok := matchQuantity(resp.RemainingQuantity)
	if !ok || resp.Found != remaining.IsPositive() || remaining.GreaterThan(o.Quantity) {
		return nil, invalidMatch("invalid cancelled quantity")
	}
	if !resp.Found {
		return nil, nil
	}
	return func(tx *gorm.DB) error {
		current, err := repository.GetOrderForUpdate(tx, o.OrderID)
		if err != nil {
			return err
		}
		if current.Type != model.OrderTypeLimit || current.RejectReason != "" ||
			(!current.ClosedQuantity.IsZero() && !current.ClosedQuantity.Equal(remaining)) ||
			current.FilledQuantity.Add(remaining).GreaterThan(current.Quantity) {
			return invalidMatch("cancel contradicts order state")
		}
		// Matching knows about fills whose submit transactions may still be
		// pending. Record only its cancelled quantity, never infer filled.
		current.ClosedQuantity = remaining
		if err := saveOrder(tx, &current); err != nil {
			return err
		}
		return Enqueue(tx, o.OrderID, ReleaseStep, nil)
	}, nil
}

func (s steps) release(ctx context.Context, e Entry) (func(*gorm.DB) error, error) {
	o, err := s.order(ctx, e)
	if err != nil {
		return nil, err
	}
	amount := o.ClosedQuantity
	if o.Side == model.OrderSideBuy {
		amount = o.Price.Mul(amount)
	}
	if amount.IsZero() {
		return nil, nil
	}
	_, err = s.user.Unfreeze(ctx, &userpb.UnfreezeRequest{
		UserId: o.UserID, Asset: o.FrozenAsset, Amount: amount.String(), OrderId: o.OrderID,
	})
	return nil, err
}

func (s steps) settle(ctx context.Context, e Entry) (func(*gorm.DB) error, error) {
	var req userpb.SettleTradeRequest
	if err := json.Unmarshal(e.Payload, &req); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "invalid settlement payload: %v", err)
	}
	if req.TradeId == "" || e.Step != settleKey(req.TradeId) {
		return nil, status.Error(codes.FailedPrecondition, "settlement payload does not match step")
	}
	_, err := s.user.SettleTrade(ctx, &req)
	return nil, err
}

func (s steps) order(ctx context.Context, e Entry) (model.Order, error) {
	var o model.Order
	err := s.db.WithContext(ctx).Where("order_id = ?", e.AggregateID).Take(&o).Error
	if err != nil {
		return o, fmt.Errorf("read order for %s: %w", e.Step, err)
	}
	return o, nil
}

func saveOrder(tx *gorm.DB, o *model.Order) error {
	wasFinal := o.Status.IsFinal()
	refreshStatus(o)
	if err := repository.SaveOrder(tx, o); err != nil {
		return err
	}
	if !wasFinal && o.Status.IsFinal() {
		if finals, ok := tx.Statement.Context.Value(finalStatusesKey{}).(*[]model.OrderStatus); ok {
			*finals = append(*finals, o.Status)
		}
	}
	return nil
}

func refreshStatus(o *model.Order) {
	switch {
	case o.RejectReason != "":
		o.Status = model.OrderStatusRejected
	case o.FilledQuantity.Equal(o.Quantity):
		o.Status = model.OrderStatusFilled
	case o.ClosedQuantity.IsPositive():
		o.Status = model.OrderStatusCancelled
	case o.FilledQuantity.IsPositive():
		o.Status = model.OrderStatusPartialFilled
	case o.Status != model.OrderStatusPending:
		o.Status = model.OrderStatusOpen
	}
}

func invalidMatch(message string) error {
	return status.Error(codes.FailedPrecondition, "inconsistent matching result: "+message)
}

func matchingSide(s model.OrderSide) matchingpb.OrderSide {
	switch s {
	case model.OrderSideBuy:
		return matchingpb.OrderSide_ORDER_SIDE_BUY
	case model.OrderSideSell:
		return matchingpb.OrderSide_ORDER_SIDE_SELL
	default:
		return matchingpb.OrderSide_ORDER_SIDE_UNSPECIFIED
	}
}

func matchingType(t model.OrderType) matchingpb.OrderType {
	switch t {
	case model.OrderTypeLimit:
		return matchingpb.OrderType_ORDER_TYPE_LIMIT
	case model.OrderTypeMarket:
		return matchingpb.OrderType_ORDER_TYPE_MARKET
	case model.OrderTypeIOC:
		return matchingpb.OrderType_ORDER_TYPE_IOC
	case model.OrderTypeFOK:
		return matchingpb.OrderType_ORDER_TYPE_FOK
	default:
		return matchingpb.OrderType_ORDER_TYPE_UNSPECIFIED
	}
}

func settleKey(id string) string { return settleStep + ":" + id }
