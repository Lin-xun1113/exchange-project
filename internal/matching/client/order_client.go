// Package client provides gRPC client for Order Service
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/timeout"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DefaultTimeout 默认超时时间
const DefaultTimeout = 3 * time.Second

// MaxRetries 最大重试次数
const MaxRetries = 3

// OrderClient gRPC client for order service
type OrderClient struct {
	conn   *grpc.ClientConn
	client orderpb.OrderServiceClient
}

// NewOrderClient creates a new order service gRPC client
func NewOrderClient(addr string) (*OrderClient, error) {
	conn, err := grpc.Dial(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			retry.UnaryClientInterceptor(
				retry.WithMax(MaxRetries),
				retry.WithBackoff(retry.BackoffExponential(100*time.Millisecond)),
			),
			timeout.UnaryClientInterceptor(DefaultTimeout),
			logging.UnaryClientInterceptor(interceptorLogger()),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial order service: %w", err)
	}

	logger.Info("order gRPC client connected", logger.S("addr", addr))

	return &OrderClient{
		conn:   conn,
		client: orderpb.NewOrderServiceClient(conn),
	}, nil
}

// interceptorLogger gRPC middleware 日志适配器
func interceptorLogger() logging.Logger {
	return logging.LoggerFunc(func(ctx context.Context, level logging.Level, msg string, fields ...any) {
		zapFields := make([]zap.Field, 0, len(fields))
		for _, f := range fields {
			if f == nil {
				continue
			}
			if field, ok := f.(zap.Field); ok {
				zapFields = append(zapFields, field)
			}
		}
		switch level {
		case logging.LevelDebug:
			logger.Debug(msg, zapFields...)
		case logging.LevelInfo:
			logger.Info(msg, zapFields...)
		case logging.LevelWarn:
			logger.Warn(msg, zapFields...)
		case logging.LevelError:
			logger.Error(msg, zapFields...)
		}
	})
}

// Close closes the gRPC connection
func (c *OrderClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// UpdateOrderStatus updates order status after matching
func (c *OrderClient) UpdateOrderStatus(orderID string, status orderpb.OrderStatus, filledQuantity string) error {
	_, err := c.client.UpdateOrderStatus(context.Background(), &orderpb.UpdateOrderStatusRequest{
		OrderId:         orderID,
		Status:          status,
		FilledQuantity:  filledQuantity,
	})
	return err
}
