// Package client 提供 gRPC 客户端封装
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/timeout"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// OrderClient 订单服务客户端封装
type OrderClient struct {
	conn   *grpc.ClientConn
	client orderpb.OrderServiceClient
	cb     *CircuitBreaker
}

// NewOrderClient 创建订单服务客户端
func NewOrderClient(addr string) (*OrderClient, error) {
	// 创建熔断器
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig("order-service"))

	conn, err := grpc.Dial(addr,
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			circuitBreakerInterceptor(cb, "order"),
			logInterceptor,
			timeout.UnaryClientInterceptor(DefaultTimeout),
			retry.UnaryClientInterceptor(
				retry.WithMax(MaxRetries),
				retry.WithBackoff(retry.BackoffExponential(100*time.Millisecond)),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial order service: %w", err)
	}

	logger.Info("order gRPC client connected", logger.S("addr", addr))

	return &OrderClient{
		conn:   conn,
		client: orderpb.NewOrderServiceClient(conn),
		cb:     cb,
	}, nil
}

// Close 关闭连接
func (c *OrderClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// CreateOrder 创建订单
func (c *OrderClient) CreateOrder(ctx context.Context, req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
	return c.client.CreateOrder(ctx, req)
}

// CancelOrder 取消订单
func (c *OrderClient) CancelOrder(ctx context.Context, req *orderpb.CancelOrderRequest) (*orderpb.CancelOrderResponse, error) {
	return c.client.CancelOrder(ctx, req)
}

// GetOrder 获取订单
func (c *OrderClient) GetOrder(ctx context.Context, req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
	return c.client.GetOrder(ctx, req)
}

// ListOrders 订单列表
func (c *OrderClient) ListOrders(ctx context.Context, req *orderpb.ListOrdersRequest) (*orderpb.ListOrdersResponse, error) {
	return c.client.ListOrders(ctx, req)
}

// UpdateOrderStatus 更新订单状态（内部使用）
func (c *OrderClient) UpdateOrderStatus(ctx context.Context, req *orderpb.UpdateOrderStatusRequest) (*orderpb.UpdateOrderStatusResponse, error) {
	return c.client.UpdateOrderStatus(ctx, req)
}
