// Package server provides gRPC service implementation
package server

import (
	"context"

	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/service"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// OrderServer order service gRPC server implementation
type OrderServer struct {
	orderpb.UnimplementedOrderServiceServer
	svc *service.OrderService
}

// NewOrderServer creates order service gRPC server
func NewOrderServer(svc *service.OrderService) *OrderServer {
	return &OrderServer{
		svc: svc,
	}
}

// CreateOrder creates order
func (s *OrderServer) CreateOrder(ctx context.Context, req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
	price, err := decimal.NewFromString(req.Price)
	if err != nil {
		logger.WithContext(ctx).Error("invalid price", zap.String("price", req.Price))
		return nil, err
	}

	quantity, err := decimal.NewFromString(req.Quantity)
	if err != nil {
		logger.WithContext(ctx).Error("invalid quantity", zap.String("quantity", req.Quantity))
		return nil, err
	}

	orderReq := &service.CreateOrderRequest{
		UserID:         req.UserId,
		Symbol:         req.Symbol,
		Side:           model.OrderSide(req.Side.String()),
		OrderType:      model.OrderType(req.OrderType.String()),
		Price:          price,
		Quantity:       quantity,
		IdempotencyKey: req.IdempotencyKey,
	}

	order, err := s.svc.CreateOrder(ctx, orderReq)
	if err != nil {
		logger.WithContext(ctx).Error("CreateOrder failed",
			logger.I64("user_id", req.UserId),
			logger.S("symbol", req.Symbol),
			zap.Error(err),
		)
		return nil, err
	}

	return &orderpb.CreateOrderResponse{
		Order: modelToProtoOrder(order),
	}, nil
}

// CancelOrder cancels order
func (s *OrderServer) CancelOrder(ctx context.Context, req *orderpb.CancelOrderRequest) (*orderpb.CancelOrderResponse, error) {
	order, err := s.svc.CancelOrder(ctx, req.OrderId, req.UserId)
	if err != nil {
		logger.WithContext(ctx).Error("CancelOrder failed",
			logger.S("order_id", req.OrderId),
			logger.I64("user_id", req.UserId),
			zap.Error(err),
		)
		return nil, err
	}

	return &orderpb.CancelOrderResponse{
		Success: true,
		Order:   modelToProtoOrder(order),
	}, nil
}

// GetOrder gets order
func (s *OrderServer) GetOrder(ctx context.Context, req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
	order, err := s.svc.GetOrder(ctx, req.OrderId)
	if err != nil {
		logger.WithContext(ctx).Error("GetOrder failed", logger.S("order_id", req.OrderId), zap.Error(err))
		return nil, err
	}

	return &orderpb.GetOrderResponse{
		Order: modelToProtoOrder(order),
	}, nil
}

// ListOrders lists orders
func (s *OrderServer) ListOrders(ctx context.Context, req *orderpb.ListOrdersRequest) (*orderpb.ListOrdersResponse, error) {
	status := req.Status.String()
	orders, total, err := s.svc.ListOrders(ctx, req.UserId, req.Symbol, status, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.WithContext(ctx).Error("ListOrders failed",
			logger.I64("user_id", req.UserId),
			logger.S("symbol", req.Symbol),
			zap.Error(err),
		)
		return nil, err
	}

	protoOrders := make([]*orderpb.Order, len(orders))
	for i, order := range orders {
		protoOrders[i] = modelToProtoOrder(order)
	}

	return &orderpb.ListOrdersResponse{
		Orders:   protoOrders,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}, nil
}

// UpdateOrderStatus updates order status (called by matching engine)
func (s *OrderServer) UpdateOrderStatus(ctx context.Context, req *orderpb.UpdateOrderStatusRequest) (*orderpb.UpdateOrderStatusResponse, error) {
	filledQty, err := decimal.NewFromString(req.FilledQuantity)
	if err != nil {
		logger.WithContext(ctx).Error("invalid filled_quantity", zap.String("filled_quantity", req.FilledQuantity))
		return nil, err
	}

	err = s.svc.UpdateFilledQuantity(ctx, req.OrderId, filledQty.InexactFloat64())
	if err != nil {
		logger.WithContext(ctx).Error("UpdateOrderStatus failed",
			logger.S("order_id", req.OrderId),
			zap.Error(err),
		)
		return nil, err
	}

	order, err := s.svc.GetOrder(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}

	return &orderpb.UpdateOrderStatusResponse{
		Success: true,
		Order:   modelToProtoOrder(order),
	}, nil
}

// modelToProtoOrder converts model.Order to proto Order
func modelToProtoOrder(order *model.Order) *orderpb.Order {
	if order == nil {
		return nil
	}

	var status orderpb.OrderStatus
	switch order.Status {
	case "pending":
		status = orderpb.OrderStatus_ORDER_STATUS_PENDING
	case "partial_filled":
		status = orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED
	case "filled":
		status = orderpb.OrderStatus_ORDER_STATUS_FILLED
	case "cancelled":
		status = orderpb.OrderStatus_ORDER_STATUS_CANCELLED
	case "rejected":
		status = orderpb.OrderStatus_ORDER_STATUS_REJECTED
	default:
		status = orderpb.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}

	var side orderpb.OrderSide
	switch order.Side {
	case "buy":
		side = orderpb.OrderSide_ORDER_SIDE_BUY
	case "sell":
		side = orderpb.OrderSide_ORDER_SIDE_SELL
	default:
		side = orderpb.OrderSide_ORDER_SIDE_UNSPECIFIED
	}

	var orderType orderpb.OrderType
	switch order.OrderType {
	case "limit":
		orderType = orderpb.OrderType_ORDER_TYPE_LIMIT
	case "market":
		orderType = orderpb.OrderType_ORDER_TYPE_MARKET
	default:
		orderType = orderpb.OrderType_ORDER_TYPE_UNSPECIFIED
	}

	return &orderpb.Order{
		Id:                order.ID,
		OrderId:           order.OrderID,
		UserId:            order.UserID,
		Symbol:            order.Symbol,
		Side:              side,
		OrderType:         orderType,
		Price:             decimal.NewFromFloat(order.Price).String(),
		Quantity:          decimal.NewFromFloat(order.Quantity).String(),
		FilledQuantity:    decimal.NewFromFloat(order.FilledQuantity).String(),
		RemainingQuantity: decimal.NewFromFloat(order.Quantity - order.FilledQuantity).String(),
		Status:            status,
		CreatedAt:         timestamppb.New(order.CreatedAt),
		UpdatedAt:         timestamppb.New(order.UpdatedAt),
	}
}
