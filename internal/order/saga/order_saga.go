// Package saga implements the order saga orchestrator pattern.
package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/outbox"
	"github.com/linxun2025/exchange-project/pkg/errors"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// OrderRepositoryInterface 订单仓储接口
type OrderRepositoryInterface interface {
	GetByIdempotencyKey(ctx context.Context, key string) (*model.Order, error)
	GetByOrderID(ctx context.Context, orderID string) (*model.Order, error)
	UpdateStatus(ctx context.Context, orderID string, status model.OrderStatus, filledQty float64) error
}

// OrderSaga 订单Saga编排器
type OrderSaga struct {
	db         *gorm.DB
	orderRepo  OrderRepositoryInterface
	outboxRepo outbox.Repository
}

// NewOrderSaga 创建订单Saga编排器
func NewOrderSaga(db *gorm.DB, orderRepo OrderRepositoryInterface, outboxRepo outbox.Repository) *OrderSaga {
	return &OrderSaga{
		db:         db,
		orderRepo:  orderRepo,
		outboxRepo: outboxRepo,
	}
}

// SagaResult Saga执行结果
type SagaResult struct {
	Success      bool
	OrderID      string
	SagaID      string
	Status      string
	Error       error
}

// CreateOrderRequest 创建订单Saga请求
type CreateOrderRequest struct {
	UserID         int64
	Symbol         string
	Side           model.OrderSide
	OrderType      model.OrderType
	Price          decimal.Decimal
	Quantity       decimal.Decimal
	IdempotencyKey string
}

// Step 枚举Saga步骤
type Step string

const (
	StepFreezeBalance   Step = "freeze_balance"
	StepSubmitMatching  Step = "submit_matching"
	StepUpdateStatus    Step = "update_status"
	StepUnfreezeBalance Step = "unfreeze_balance"
)

// CreateOrderSaga 执行创建订单Saga
func (s *OrderSaga) CreateOrderSaga(ctx context.Context, req *CreateOrderRequest) (*SagaResult, error) {
	// 1. 幂等性检查：如果已有相同idempotency_key的订单，直接返回
	if req.IdempotencyKey != "" {
		existing, err := s.orderRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
		if err == nil && existing != nil {
			logger.Info("idempotent request, returning existing order",
				logger.S("idempotency_key", req.IdempotencyKey),
				logger.S("order_id", existing.OrderID),
			)
			return &SagaResult{
				Success: true,
				OrderID: existing.OrderID,
				SagaID:  req.IdempotencyKey,
				Status:  string(existing.Status),
			}, nil
		}

		// 新增：检查 outbox 表是否有进行中的 saga
		outboxEntries, err := s.outboxRepo.GetBySagaID(ctx, req.IdempotencyKey)
		if err == nil && len(outboxEntries) > 0 {
			// 检查是否有未完成的条目
			for _, entry := range outboxEntries {
				if entry.Status != outbox.StatusDone {
					logger.Info("in-progress saga found, returning in_progress",
						logger.S("idempotency_key", req.IdempotencyKey),
					)
					return &SagaResult{
						Success: true,
						OrderID: "", // 还在进行中，没有订单ID
						SagaID:  req.IdempotencyKey,
						Status:  "in_progress",
					}, nil
				}
			}
		}
	}

	// 2. 开始Saga：创建订单记录
	orderID := generateOrderID()
	order := &model.Order{
		OrderID:        orderID,
		IdempotencyKey: req.IdempotencyKey,
		UserID:         req.UserID,
		Symbol:         req.Symbol,
		Side:           string(req.Side),
		OrderType:      string(req.OrderType),
		Price:          req.Price.InexactFloat64(),
		Quantity:       req.Quantity.InexactFloat64(),
		FilledQuantity: 0,
		Status:         string(model.OrderStatusCreated),
	}

	// 3. 在事务中创建订单和outbox条目
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 创建订单
		if err := tx.Create(order).Error; err != nil {
			return fmt.Errorf("failed to create order: %w", err)
		}

		// 计算冻结金额
		freezeAmount := req.Price.Mul(req.Quantity).InexactFloat64()

		// 创建冻结余额的outbox条目
		freezePayload := outbox.FreezeBalancePayload{
			UserID:  req.UserID,
			Amount:  freezeAmount,
			OrderID: orderID,
		}
		payloadBytes, err := json.Marshal(freezePayload)
		if err != nil {
			return fmt.Errorf("failed to marshal freeze payload: %w", err)
		}

		outboxEntry := &outbox.OutboxEntry{
			SagaID:     req.IdempotencyKey,
			StepName:   string(StepFreezeBalance),
			ActionType: outbox.ActionFreezeBalance,
			Payload:    string(payloadBytes),
			Status:     outbox.StatusPending,
			MaxRetries: 5,
		}

		if err := tx.Create(outboxEntry).Error; err != nil {
			return fmt.Errorf("failed to create outbox entry: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	logger.Info("order saga started",
		logger.S("saga_id", req.IdempotencyKey),
		logger.S("order_id", orderID),
		logger.S("symbol", req.Symbol),
		logger.I64("user_id", req.UserID),
	)

	return &SagaResult{
		Success: true,
		OrderID: orderID,
		SagaID:  req.IdempotencyKey,
		Status:  string(model.OrderStatusCreated),
	}, nil
}

// HandleFreezeBalanceSuccess 处理冻结余额成功
func (s *OrderSaga) HandleFreezeBalanceSuccess(ctx context.Context, sagaID, orderID string, freezePayload *outbox.FreezeBalancePayload) error {
	// 更新订单状态为frozen
	metrics.GetMetrics().RecordSagaStateTransition(string(model.OrderStatusCreated), string(model.OrderStatusFrozen))
	err := s.orderRepo.UpdateStatus(ctx, orderID, model.OrderStatusFrozen, 0)
	if err != nil {
		return fmt.Errorf("failed to update order status to frozen: %w", err)
	}

	// 创建提交撮合的outbox条目
	order, err := s.orderRepo.GetByOrderID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get order: %w", err)
	}

	matchingPayload := outbox.SubmitMatchingPayload{
		OrderID:   orderID,
		UserID:    order.UserID,
		Symbol:    order.Symbol,
		Side:      order.Side,
		OrderType: order.OrderType,
		Price:     fmt.Sprintf("%.8f", order.Price),
		Quantity:  fmt.Sprintf("%.8f", order.Quantity),
	}
	payloadBytes, _ := json.Marshal(matchingPayload)

	outboxEntry := &outbox.OutboxEntry{
		SagaID:     sagaID,
		StepName:   string(StepSubmitMatching),
		ActionType: outbox.ActionSubmitMatching,
		Payload:    string(payloadBytes),
		Status:     outbox.StatusPending,
		MaxRetries: 5,
	}

	return s.outboxRepo.Create(ctx, outboxEntry)
}

// HandleSubmitMatchingSuccess 处理撮合提交成功
func (s *OrderSaga) HandleSubmitMatchingSuccess(ctx context.Context, sagaID, orderID string) error {
	// 更新订单状态为submitted（已提交到撮合引擎）
	metrics.GetMetrics().RecordSagaStateTransition(string(model.OrderStatusFrozen), string(model.OrderStatusSubmitted))
	return s.orderRepo.UpdateStatus(ctx, orderID, model.OrderStatusSubmitted, 0)
}

// HandleMatchingResult 处理撮合结果
func (s *OrderSaga) HandleMatchingResult(ctx context.Context, sagaID, orderID string, filledQty float64, isComplete bool) error {
	order, err := s.orderRepo.GetByOrderID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get order: %w", err)
	}

	// 验证状态转换
	currentStatus := model.OrderStatus(order.Status)
	if !currentStatus.CanTransitionTo(model.OrderStatusPartialFilled) && !currentStatus.CanTransitionTo(model.OrderStatusFilled) {
		return fmt.Errorf("invalid state transition from %s to filled/partial_filled", order.Status)
	}

	var newStatus model.OrderStatus
	if isComplete {
		newStatus = model.OrderStatusFilled
		metrics.GetMetrics().RecordSagaStateTransition(order.Status, string(model.OrderStatusFilled))
	} else {
		newStatus = model.OrderStatusPartialFilled
		metrics.GetMetrics().RecordSagaStateTransition(order.Status, string(model.OrderStatusPartialFilled))
	}

	return s.orderRepo.UpdateStatus(ctx, orderID, newStatus, filledQty)
}

// HandleMatchingFailure 处理撮合失败 - 执行补偿
func (s *OrderSaga) HandleMatchingFailure(ctx context.Context, sagaID, orderID string, freezePayload *outbox.FreezeBalancePayload) error {
	// 更新订单状态为rejected（从frozen状态转移）
	metrics.GetMetrics().RecordSagaStateTransition(string(model.OrderStatusFrozen), string(model.OrderStatusRejected))
	if err := s.orderRepo.UpdateStatus(ctx, orderID, model.OrderStatusRejected, 0); err != nil {
		return fmt.Errorf("failed to update order status to rejected: %w", err)
	}

	// 创建解冻余额的outbox条目进行补偿
	unfreezePayload := outbox.UnfreezeBalancePayload{
		UserID:  freezePayload.UserID,
		Amount:  freezePayload.Amount,
		OrderID: freezePayload.OrderID,
	}
	payloadBytes, _ := json.Marshal(unfreezePayload)

	outboxEntry := &outbox.OutboxEntry{
		SagaID:     sagaID,
		StepName:   string(StepUnfreezeBalance),
		ActionType: outbox.ActionUnfreezeBalance,
		Payload:    string(payloadBytes),
		Status:     outbox.StatusPending,
		MaxRetries: 5,
	}

	return s.outboxRepo.Create(ctx, outboxEntry)
}

// HandleUnfreezeSuccess 处理解冻成功
func (s *OrderSaga) HandleUnfreezeSuccess(ctx context.Context, sagaID, orderID string) error {
	logger.Info("compensation completed: unfreeze balance",
		logger.S("saga_id", sagaID),
		logger.S("order_id", orderID),
	)
	return nil
}

// CancelOrderSaga 取消订单Saga
func (s *OrderSaga) CancelOrderSaga(ctx context.Context, orderID string, userID int64) (*SagaResult, error) {
	// 获取订单
	order, err := s.orderRepo.GetByOrderID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	// 验证权限
	if order.UserID != userID {
		return nil, errors.ErrForbidden
	}

	// 检查订单状态是否允许取消
	currentStatus := model.OrderStatus(order.Status)
	if !currentStatus.CanTransitionTo(model.OrderStatusCancelled) {
		return nil, errors.ErrOrderInvalid
	}

	// 记录状态转移
	metrics.GetMetrics().RecordSagaStateTransition(order.Status, string(model.OrderStatusCancelled))

	// 计算需要解冻的金额
	remainingQty := order.Quantity - order.FilledQuantity
	freezeAmount := decimal.NewFromFloat(order.Price).Mul(decimal.NewFromFloat(remainingQty)).InexactFloat64()

	// 使用基于订单ID的固定sagaID，确保同一订单多次取消操作可以幂等处理
	sagaID := fmt.Sprintf("cancel_%s", orderID)
	unfreezePayload := outbox.UnfreezeBalancePayload{
		UserID:  userID,
		Amount:  freezeAmount,
		OrderID: orderID,
	}
	payloadBytes, _ := json.Marshal(unfreezePayload)

	outboxEntry := &outbox.OutboxEntry{
		SagaID:     sagaID,
		StepName:   string(StepUnfreezeBalance),
		ActionType: outbox.ActionUnfreezeBalance,
		Payload:    string(payloadBytes),
		Status:     outbox.StatusPending,
		MaxRetries: 5,
	}

	// 在事务中更新状态和创建outbox条目
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 更新订单状态
		if err := tx.Model(&model.Order{}).Where("order_id = ?", orderID).
			Updates(map[string]interface{}{
				"status": model.OrderStatusCancelled,
			}).Error; err != nil {
			return fmt.Errorf("failed to update order status: %w", err)
		}

		// 创建outbox条目
		if err := tx.Create(outboxEntry).Error; err != nil {
			return fmt.Errorf("failed to create outbox entry: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	logger.Info("cancel order saga started",
		logger.S("saga_id", sagaID),
		logger.S("order_id", orderID),
	)

	return &SagaResult{
		Success: true,
		OrderID: orderID,
		SagaID:  sagaID,
		Status:  string(model.OrderStatusCancelled),
	}, nil
}

// GetSagaStatus 获取Saga状态
func (s *OrderSaga) GetSagaStatus(ctx context.Context, sagaID string) (*SagaResult, error) {
	entries, err := s.outboxRepo.GetBySagaID(ctx, sagaID)
	if err != nil {
		return nil, fmt.Errorf("failed to get saga entries: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("saga not found: %s", sagaID)
	}

	// 检查是否有任何条目失败
	for _, entry := range entries {
		if entry.Status == outbox.StatusDeadLetter {
			return &SagaResult{
				Success: false,
				SagaID:  sagaID,
				Status:  string(outbox.StatusDeadLetter),
				Error:   fmt.Errorf("saga has dead letter entries"),
			}, nil
		}
	}

	// 检查是否所有条目都已完成
	allDone := true
	for _, entry := range entries {
		if entry.Status != outbox.StatusDone {
			allDone = false
			break
		}
	}

	if allDone {
		return &SagaResult{
			Success: true,
			SagaID:  sagaID,
			Status:  "completed",
		}, nil
	}

	return &SagaResult{
		Success: false,
		SagaID:  sagaID,
		Status:  "in_progress",
	}, nil
}

// generateOrderID 生成订单ID
func generateOrderID() string {
	return fmt.Sprintf("ORD%s%d", time.Now().Format("20060102150405"), time.Now().UnixNano()%1000000)
}
