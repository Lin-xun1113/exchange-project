// Package service 提供订单服务层实现
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	userrepo "github.com/linxun2025/exchange-project/internal/user/repository"
	"github.com/linxun2025/exchange-project/pkg/errors"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/shopspring/decimal"
)

// OrderService 订单服务
type OrderService struct {
	orderRepo *repository.OrderRepository
	userRepo  *userrepo.UserRepository
	redis     *redis.Client
}

// NewOrderService 创建订单服务
func NewOrderService(orderRepo *repository.OrderRepository, userRepo *userrepo.UserRepository, redisClient *redis.Client) *OrderService {
	return &OrderService{
		orderRepo: orderRepo,
		userRepo:  userRepo,
		redis:     redisClient,
	}
}

// CreateOrderRequest 创建订单请求
type CreateOrderRequest struct {
	UserID         int64
	Symbol         string
	Side           model.OrderSide
	OrderType      model.OrderType
	Price          decimal.Decimal
	Quantity       decimal.Decimal
	IdempotencyKey string
}

// CreateOrder 创建订单（幂等）
func (s *OrderService) CreateOrder(ctx context.Context, req *CreateOrderRequest) (*model.Order, error) {
	// 1. 幂等检查
	if req.IdempotencyKey != "" {
		idempotencyKey := fmt.Sprintf("idempotency:order:%s", req.IdempotencyKey)

		existing, err := s.redis.Get(ctx, idempotencyKey).Result()
		if err == nil {
			var order model.Order
			if err := json.Unmarshal([]byte(existing), &order); err == nil {
				logger.Info("idempotent request, returning cached order",
					logger.S("idempotency_key", req.IdempotencyKey),
					logger.S("order_id", order.OrderID),
				)
				return &order, nil
			}
		}

		existingOrder, err := s.orderRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
		if err == nil && existingOrder != nil {
			return existingOrder, nil
		}
	}

	// 2. 分布式锁防止并发重复创建
	lockKey := fmt.Sprintf("lock:order:create:%d:%s", req.UserID, req.Symbol)
	locked, err := s.redis.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
	if err != nil || !locked {
		return nil, errors.New(errors.CodeConflict, "concurrent request")
	}
	defer s.redis.Del(ctx, lockKey)

	// 3. 检查余额
	available, _, err := s.userRepo.GetBalance(ctx, req.UserID)
	if err != nil {
		return nil, err
	}

	freezeAmount := req.Price.Mul(req.Quantity)
	if req.Side == model.OrderSideBuy && available < freezeAmount.InexactFloat64() {
		return nil, errors.ErrBalance
	}

	// 4. 冻结余额
	if err := s.userRepo.FreezeAmount(ctx, req.UserID, freezeAmount.InexactFloat64()); err != nil {
		return nil, err
	}

	// 5. 创建订单
	orderID := generateOrderID()
	order := &model.Order{
		OrderID:        orderID,
		IdempotencyKey: req.IdempotencyKey,
		UserID:         req.UserID,
		Symbol:         req.Symbol,
		Side:           string(req.Side),
		OrderType:      string(req.OrderType),
		Price:          req.Price.InexactFloat64(),
		Quantity:        req.Quantity.InexactFloat64(),
		FilledQuantity: 0,
		Status:         string(model.OrderStatusPending),
	}

	if err := s.orderRepo.Create(ctx, order); err != nil {
		s.userRepo.UnfreezeAmount(ctx, req.UserID, freezeAmount.InexactFloat64())
		return nil, err
	}

	// 6. 写入幂等缓存
	if req.IdempotencyKey != "" {
		idempotencyKey := fmt.Sprintf("idempotency:order:%s", req.IdempotencyKey)
		if data, err := json.Marshal(order); err == nil {
			s.redis.Set(ctx, idempotencyKey, data, 24*time.Hour)
		}
	}

	logger.Info("order created",
		logger.S("order_id", orderID),
		logger.I64("user_id", req.UserID),
		logger.S("symbol", req.Symbol),
		logger.S("side", string(req.Side)),
		logger.S("price", req.Price.String()),
		logger.S("quantity", req.Quantity.String()),
	)

	metrics.GetMetrics().RecordOrderCreated(string(req.Side), req.Symbol)

	return order, nil
}

// CancelOrder 取消订单
func (s *OrderService) CancelOrder(ctx context.Context, orderID string, userID int64) (*model.Order, error) {
	// 分布式锁防止并发取消
	lockKey := fmt.Sprintf("lock:order:cancel:%s", orderID)
	locked, err := s.redis.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
	if err != nil || !locked {
		return nil, errors.New(errors.CodeConflict, "concurrent request")
	}
	defer s.redis.Del(ctx, lockKey)

	order, err := s.orderRepo.GetByOrderID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.UserID != userID {
		return nil, errors.ErrForbidden
	}

	if order.Status != string(model.OrderStatusPending) && order.Status != string(model.OrderStatusPartialFilled) {
		return nil, errors.ErrOrderInvalid
	}

	remainingQty := order.Quantity - order.FilledQuantity
	if remainingQty > 0 {
		freezeAmount := decimal.NewFromFloat(order.Price).Mul(decimal.NewFromFloat(remainingQty))
		if err := s.userRepo.UnfreezeAmount(ctx, userID, freezeAmount.InexactFloat64()); err != nil {
			return nil, err
		}
	}

	if err := s.orderRepo.UpdateStatus(ctx, orderID, model.OrderStatusCancelled, order.FilledQuantity); err != nil {
		return nil, err
	}

	order.Status = string(model.OrderStatusCancelled)
	logger.Info("order cancelled",
		logger.S("order_id", orderID),
		logger.I64("user_id", userID),
	)

	metrics.GetMetrics().RecordOrderCancelled(order.Side, order.Symbol)

	return order, nil
}

// GetOrder 获取订单
func (s *OrderService) GetOrder(ctx context.Context, orderID string) (*model.Order, error) {
	return s.orderRepo.GetByOrderID(ctx, orderID)
}

// ListOrders 列出订单
func (s *OrderService) ListOrders(ctx context.Context, userID int64, symbol, status string, page, pageSize int) ([]*model.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	return s.orderRepo.ListByUserID(ctx, userID, symbol, status, page, pageSize)
}

// UpdateFilledQuantity 更新成交数量
func (s *OrderService) UpdateFilledQuantity(ctx context.Context, orderID string, filledQty float64) error {
	order, err := s.orderRepo.GetByOrderID(ctx, orderID)
	if err != nil {
		return err
	}

	var status model.OrderStatus
	if filledQty >= order.Quantity {
		status = model.OrderStatusFilled
	} else {
		status = model.OrderStatusPartialFilled
	}

	err = s.orderRepo.UpdateStatus(ctx, orderID, status, filledQty)
	if err != nil {
		return err
	}

	if order.Quantity > 0 {
		fillRate := filledQty / order.Quantity
		metrics.GetMetrics().RecordOrderFillRate(order.Side, order.Symbol, fillRate)
	}

	return nil
}

// RecordOrderFillRate 记录订单成交率（供外部调用）
func (s *OrderService) RecordOrderFillRate(orderID string, filledQty, totalQty float64) {
	if totalQty > 0 {
		fillRate := filledQty / totalQty
		metrics.GetMetrics().RecordOrderFillRate("unknown", "unknown", fillRate)
	}
}

// generateOrderID 生成订单ID
func generateOrderID() string {
	return fmt.Sprintf("ORD%s%s", time.Now().Format("20060102150405"), uuid.New().String()[:8])
}
