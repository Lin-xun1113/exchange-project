// Package client 提供 gRPC 客户端封装
package client

import (
	"context"
	"fmt"
	"time"

	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// UserClient 用户服务客户端封装
type UserClient struct {
	conn   *grpc.ClientConn
	client userpb.UserServiceClient
	cb     *CircuitBreaker
}

// NewUserClient 创建用户服务客户端
func NewUserClient(addr string) (*UserClient, error) {
	// 创建熔断器
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig("user-service"))

	conn, err := grpc.Dial(addr,
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			userCircuitBreakerInterceptor(cb, "user"),
			userLogInterceptor,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial user service: %w", err)
	}

	logger.Info("user gRPC client connected", logger.S("addr", addr))

	return &UserClient{
		conn:   conn,
		client: userpb.NewUserServiceClient(conn),
		cb:     cb,
	}, nil
}

// userCircuitBreakerInterceptor 熔断器拦截器
func userCircuitBreakerInterceptor(cb *CircuitBreaker, clientName string) grpc.UnaryClientInterceptor {
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

		logger.Debug("gRPC client request",
			logger.S("client", clientName),
			logger.S("method", method),
			logger.S("status", statusStr),
			logger.I64("latency_ms", duration.Milliseconds()),
		)

		return err
	}
}

// userLogInterceptor 日志拦截器
func userLogInterceptor(
	ctx context.Context,
	method string,
	req interface{},
	reply interface{},
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	logger.Debug("gRPC request",
		logger.S("method", method),
		logger.S("target", cc.Target()),
	)

	err := invoker(ctx, method, req, reply, cc, opts...)

	if err != nil {
		st, _ := status.FromError(err)
		logger.Error("gRPC request failed",
			logger.S("method", method),
			logger.S("status", st.Code().String()),
			logger.S("message", st.Message()),
			zap.Error(err),
		)
	} else {
		logger.Debug("gRPC request success",
			logger.S("method", method),
		)
	}

	return err
}

// Close 关闭连接
func (c *UserClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Login 用户登录
func (c *UserClient) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	return c.client.Login(ctx, req)
}

// GetUser 获取用户信息
func (c *UserClient) GetUser(ctx context.Context, req *userpb.GetUserRequest) (*userpb.GetUserResponse, error) {
	return c.client.GetUser(ctx, req)
}

// CreateUser 创建用户
func (c *UserClient) CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
	return c.client.CreateUser(ctx, req)
}

// GetBalance 获取用户余额
func (c *UserClient) GetBalance(ctx context.Context, req *userpb.GetBalanceRequest) (*userpb.GetBalanceResponse, error) {
	return c.client.GetBalance(ctx, req)
}

// FreezeAmount 冻结余额
func (c *UserClient) FreezeAmount(ctx context.Context, req *userpb.FreezeAmountRequest) (*userpb.FreezeAmountResponse, error) {
	return c.client.FreezeAmount(ctx, req)
}

// UnfreezeAmount 解冻余额
func (c *UserClient) UnfreezeAmount(ctx context.Context, req *userpb.UnfreezeAmountRequest) (*userpb.UnfreezeAmountResponse, error) {
	return c.client.UnfreezeAmount(ctx, req)
}

// DeductAmount 扣减余额
func (c *UserClient) DeductAmount(ctx context.Context, req *userpb.DeductAmountRequest) (*userpb.DeductAmountResponse, error) {
	return c.client.DeductAmount(ctx, req)
}

// AddAmount 增加余额
func (c *UserClient) AddAmount(ctx context.Context, req *userpb.AddAmountRequest) (*userpb.AddAmountResponse, error) {
	return c.client.AddAmount(ctx, req)
}
