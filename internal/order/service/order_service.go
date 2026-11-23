// Package service implements the order lifecycle and queries.
package service

import (
	"context"
	"errors"
	"regexp"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	"github.com/linxun2025/exchange-project/internal/order/workflow"
	"github.com/linxun2025/exchange-project/pkg/dberr"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/money"
	"github.com/linxun2025/exchange-project/pkg/symbol"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

var idempotencyKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type OrderService struct {
	db      *gorm.DB
	allowed func(symbol.Pair) bool
	metrics *metrics.Order
}

func New(db *gorm.DB, symbols []string, m *metrics.Order) *OrderService {
	return &OrderService{db: db, allowed: symbol.Allowed(symbols), metrics: m}
}

type CreateOrderRequest struct {
	UserID         int64
	Symbol         string
	Side           model.OrderSide
	Type           model.OrderType
	Price          string
	Quantity       string
	IdempotencyKey string
}

func (s *OrderService) CreateOrder(ctx context.Context, req CreateOrderRequest) (o model.Order, replayed bool, err error) {
	o, err = s.validateCreate(req)
	if err != nil {
		return model.Order{}, false, err
	}
	o.OrderID = uuid.Must(uuid.NewV7()).String()
	db := s.db.WithContext(ctx)
	err = repository.Transaction(db, func(tx *gorm.DB) error {
		o.ID = 0 // Let MySQL allocate the ID on each transaction attempt.
		if err := repository.CreateOrder(tx, &o); err != nil {
			return err
		}
		return workflow.Enqueue(tx, o.OrderID, workflow.FreezeStep, nil)
	})
	if dberr.IsDuplicate(err) {
		previous, lookupErr := repository.FindByIdempotencyKey(db, req.UserID, req.IdempotencyKey)
		if lookupErr != nil {
			return model.Order{}, false, databaseError(ctx, "read order replay", lookupErr)
		}
		if previous.Symbol != o.Symbol || previous.Side != o.Side || previous.Type != o.Type ||
			!previous.Price.Equal(o.Price) || !previous.Quantity.Equal(o.Quantity) {
			return model.Order{}, false, status.Error(codes.AlreadyExists, "idempotency key reused with different parameters")
		}
		return previous, true, nil
	}
	if err != nil {
		return model.Order{}, false, databaseError(ctx, "create order", err)
	}
	if s.metrics != nil {
		s.metrics.OrderCreated(o.Symbol, string(o.Side))
	}
	return o, false, nil
}

func (s *OrderService) validateCreate(req CreateOrderRequest) (model.Order, error) {
	if req.UserID <= 0 || !idempotencyKey.MatchString(req.IdempotencyKey) {
		return model.Order{}, status.Error(codes.InvalidArgument, "positive user ID and valid idempotency key required")
	}
	pair, err := symbol.Parse(req.Symbol)
	if err != nil || !s.allowed(pair) {
		return model.Order{}, status.Error(codes.InvalidArgument, "unsupported symbol")
	}
	if req.Side != model.OrderSideBuy && req.Side != model.OrderSideSell {
		return model.Order{}, status.Error(codes.InvalidArgument, "invalid side")
	}
	var price decimal.Decimal
	switch req.Type {
	case model.OrderTypeMarket:
		if req.Side == model.OrderSideBuy {
			return model.Order{}, status.Error(codes.InvalidArgument, "market buy not supported")
		}
		if req.Price != "" && req.Price != "0" {
			return model.Order{}, status.Error(codes.InvalidArgument, "market sell price must be empty or zero")
		}
		price = decimal.Zero
	case model.OrderTypeLimit, model.OrderTypeIOC, model.OrderTypeFOK:
		price, err = positiveTicks(req.Price)
		if err != nil {
			return model.Order{}, status.Error(codes.InvalidArgument, "invalid price or price outside tick range")
		}
	default:
		return model.Order{}, status.Error(codes.InvalidArgument, "invalid order type")
	}
	quantity, err := positiveTicks(req.Quantity)
	if err != nil {
		return model.Order{}, status.Error(codes.InvalidArgument, "invalid quantity or quantity outside tick range")
	}
	asset, amount := pair.Base, quantity
	if req.Side == model.OrderSideBuy {
		asset, amount = pair.Quote, price.Mul(quantity)
		// A valid tick pair can still overflow the 18 integer digits allowed
		// by DECIMAL(36,18). Keep the full precision of the product.
		if amount.Exponent() < -18 || int64(amount.NumDigits())+int64(amount.Exponent()) > 18 {
			return model.Order{}, status.Error(codes.InvalidArgument, "buy notional exceeds DECIMAL(36,18) range")
		}
	}
	return model.Order{
		UserID: req.UserID, IdempotencyKey: req.IdempotencyKey, Symbol: pair.String(), Side: req.Side, Type: req.Type,
		Price: price, Quantity: quantity, FrozenAsset: asset, FrozenAmount: amount, Status: model.OrderStatusPending,
	}, nil
}

func positiveTicks(raw string) (decimal.Decimal, error) {
	value, err := money.ParsePositive(raw)
	if err != nil {
		return decimal.Zero, err
	}
	_, err = money.ToTicks(value)
	return value, err
}

func (s *OrderService) CancelOrder(ctx context.Context, userID int64, orderID string) (model.Order, error) {
	if err := validateOrderRef(userID, orderID); err != nil {
		return model.Order{}, err
	}
	var o model.Order
	err := repository.Transaction(s.db.WithContext(ctx), func(tx *gorm.DB) error {
		current, err := repository.GetOrderForUpdate(tx, orderID)
		if err != nil {
			return err
		}
		if current.UserID != userID {
			return gorm.ErrRecordNotFound
		}
		o = current
		if o.Status.IsFinal() {
			return status.Error(codes.FailedPrecondition, "order already final")
		}
		if o.CancelRequested {
			return nil
		}
		o.CancelRequested = true
		if err := repository.SaveOrder(tx, &o); err != nil {
			return err
		}
		if o.Status == model.OrderStatusOpen || o.Status == model.OrderStatusPartialFilled {
			return workflow.Enqueue(tx, o.OrderID, workflow.CancelStep, nil)
		}
		return nil
	})
	if err != nil {
		return model.Order{}, orderError(ctx, "cancel order", err)
	}
	return o, nil
}

func validateOrderRef(userID int64, orderID string) error {
	if userID <= 0 || orderID == "" || len(orderID) > 64 || !utf8.ValidString(orderID) {
		return status.Error(codes.InvalidArgument, "positive user ID and valid order ID required")
	}
	return nil
}

func orderError(ctx context.Context, operation string, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.NotFound, "order not found")
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return databaseError(ctx, operation, err)
}

func databaseError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	logger.WithContext(ctx).Error(operation, logger.Err(err))
	return status.Error(codes.Unavailable, "order database unavailable")
}
