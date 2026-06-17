// Package client provides gRPC 客户端封装
package client

import (
	"context"
	"fmt"
	"time"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/sony/gobreaker"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// MatchingClient 撮合服务客户端封装
type MatchingClient struct {
	conn   *grpc.ClientConn
	client matchingpb.MatchingServiceClient
	cb     *CircuitBreaker
}

// NewMatchingClient 创建撮合服务客户端
func NewMatchingClient(addr string) (*MatchingClient, error) {
	// 创建熔断器
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig("matching-service"))

	conn, err := grpc.Dial(addr,
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithChainUnaryInterceptor(
			grpcx.UnaryClientRequestID(),
			matchingCircuitBreakerInterceptor(cb, "matching"),
			matchingLogInterceptor,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial matching service: %w", err)
	}

	logger.Info("matching gRPC client connected", logger.S("addr", addr))

	return &MatchingClient{
		conn:   conn,
		client: matchingpb.NewMatchingServiceClient(conn),
		cb:     cb,
	}, nil
}

// matchingCircuitBreakerInterceptor 熔断器拦截器
func matchingCircuitBreakerInterceptor(cb *CircuitBreaker, clientName string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req interface{},
		reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		start := time.Now()

		err := cb.Execute(ctx, func() error {
			return invoker(ctx, method, req, reply, cc, opts...)
		})

		duration := time.Since(start)
		statusStr := "success"
		if err != nil {
			if err == gobreaker.ErrOpenState {
				statusStr = "circuit_open"
			} else if err == gobreaker.ErrTooManyRequests {
				statusStr = "circuit_busy"
			} else {
				statusStr = "error"
			}
		}
		metrics.GetMetrics().RecordGRPCClientRequest(clientName, method, statusStr)

		if err == gobreaker.ErrOpenState || err == gobreaker.ErrTooManyRequests {
			return err
		}

		logger.WithContext(ctx).Debug("gRPC client request",
			logger.S("client", clientName),
			logger.S("method", method),
			logger.S("status", statusStr),
			logger.I64("latency_ms", duration.Milliseconds()),
		)

		return err
	}
}

// matchingLogInterceptor 日志拦截器
func matchingLogInterceptor(
	ctx context.Context,
	method string,
	req interface{},
	reply interface{},
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	logger.WithContext(ctx).Debug("gRPC request",
		logger.S("method", method),
		logger.S("target", cc.Target()),
	)

	err := invoker(ctx, method, req, reply, cc, opts...)

	if err != nil {
		st, _ := status.FromError(err)
		logger.WithContext(ctx).Error("gRPC request failed",
			logger.S("method", method),
			logger.S("status", st.Code().String()),
			logger.S("message", st.Message()),
			zap.Error(err),
		)
	} else {
		logger.WithContext(ctx).Debug("gRPC request success",
			logger.S("method", method),
		)
	}

	return err
}

// Close 关闭连接
func (c *MatchingClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// SubmitOrder 提交订单到撮合引擎
func (c *MatchingClient) SubmitOrder(ctx context.Context, req *matchingpb.SubmitOrderRequest) (*matchingpb.SubmitOrderResponse, error) {
	return c.client.SubmitOrder(ctx, req)
}

// GetOrderBook 获取订单簿
func (c *MatchingClient) GetOrderBook(ctx context.Context, req *matchingpb.GetOrderBookRequest) (*matchingpb.GetOrderBookResponse, error) {
	return c.client.GetOrderBook(ctx, req)
}

// GetTrades 获取交易历史
func (c *MatchingClient) GetTrades(ctx context.Context, req *matchingpb.GetTradesRequest) (*matchingpb.GetTradesResponse, error) {
	return c.client.GetTrades(ctx, req)
}
