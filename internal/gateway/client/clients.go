// Package client 提供 gRPC 客户端封装
package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/timeout"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// DefaultTimeout 默认超时时间
const DefaultTimeout = 3 * time.Second

// MaxRetries 最大重试次数
const MaxRetries = 3

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	Name          string
	MaxRequests   uint32        // 半开状态下允许的最大请求数
	Interval      time.Duration // 关闭状态下的重置周期
	Timeout       time.Duration // 打开状态下的超时时间
	FailureThreshold uint32     // 失败阈值
}

// DefaultCircuitBreakerConfig 返回默认熔断器配置
func DefaultCircuitBreakerConfig(name string) CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Name:             name,
		MaxRequests:      3,
		Interval:         10 * time.Second,
		Timeout:          30 * time.Second,
		FailureThreshold: 5,
	}
}

// CircuitBreakerSettings 转换熔断器配置为 gobreaker.Settings
func (c *CircuitBreakerConfig) ToSettings() gobreaker.Settings {
	return gobreaker.Settings{
		Name:        c.Name,
		MaxRequests: c.MaxRequests,
		Interval:    c.Interval,
		Timeout:     c.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= c.FailureThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			// 记录熔断器状态变化
			stateValue := 0
			switch to {
			case gobreaker.StateClosed:
				stateValue = 0
			case gobreaker.StateOpen:
				stateValue = 1
			case gobreaker.StateHalfOpen:
				stateValue = 2
			}
			metrics.GetMetrics().RecordCircuitState(name, stateValue)
			logger.Warn("circuit breaker state changed",
				logger.S("name", name),
				logger.S("from", from.String()),
				logger.S("to", to.String()),
			)
		},
	}
}

// CircuitBreaker 封装 gRPC 调用的熔断器
type CircuitBreaker struct {
	*gobreaker.CircuitBreaker
	name   string
	method string
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		CircuitBreaker: gobreaker.NewCircuitBreaker(cfg.ToSettings()),
		name:            cfg.Name,
	}
}

// Execute 执行带熔断器保护的函数
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	result, err := cb.CircuitBreaker.Execute(func() (interface{}, error) {
		return nil, fn()
	})
	if err != nil {
		// 记录失败
		if err != gobreaker.ErrOpenState && err != gobreaker.ErrTooManyRequests {
			metrics.GetMetrics().RecordGRPCClientFailure(cb.name, cb.method, categorizeError(err))
		}
		return err
	}
	_ = result
	return nil
}

// categorizeError 对错误进行分类
func categorizeError(err error) string {
	if err == nil {
		return "none"
	}
	st, ok := status.FromError(err)
	if !ok {
		return "unknown"
	}
	switch st.Code() {
	case 2: // Unknown
		return "unknown"
	case 4: // DeadlineExceeded
		return "timeout"
	case 14: // Unavailable
		return "unavailable"
	case 13: // Internal
		return "internal"
	default:
		return "other"
	}
}

// unaryClientInterceptor 返回带重试和超时的 unary 拦截器链
// 返回拦截器列表供 grpc.WithChainUnaryInterceptor 使用
func unaryClientInterceptors() []grpc.UnaryClientInterceptor {
	return []grpc.UnaryClientInterceptor{
		grpcx.UnaryClientRequestID(),
		logInterceptor,
		retry.UnaryClientInterceptor(
			retry.WithMax(MaxRetries),
			retry.WithBackoff(retry.BackoffExponential(100*time.Millisecond)),
		),
		timeout.UnaryClientInterceptor(DefaultTimeout),
	}
}

// logInterceptor gRPC 日志拦截器
func logInterceptor(
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

// circuitBreakerInterceptor 创建熔断器拦截器
func circuitBreakerInterceptor(cb *CircuitBreaker, clientName string) grpc.UnaryClientInterceptor {
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

		// 记录指标
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

		// 如果是熔断器打开或过多请求，不记录延迟（因为根本没发请求）
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

// Clients 统一管理所有 gRPC 客户端
type Clients struct {
	User     *UserClient
	Order    *OrderClient
	Matching *MatchingClient
}

// Config 客户端配置
type Config struct {
	UserGRPCAddr     string
	OrderGRPCAddr    string
	MatchingGRPCAddr string
}

// NewClients 创建所有 gRPC 客户端
func NewClients(cfg *Config) (*Clients, error) {
	clients := &Clients{}
	var errs []string

	// 创建 User 客户端
	if cfg.UserGRPCAddr != "" {
		userClient, err := NewUserClient(cfg.UserGRPCAddr)
		if err != nil {
			errs = append(errs, fmt.Sprintf("user client: %v", err))
		} else {
			clients.User = userClient
		}
	}

	// 创建 Order 客户端
	if cfg.OrderGRPCAddr != "" {
		orderClient, err := NewOrderClient(cfg.OrderGRPCAddr)
		if err != nil {
			errs = append(errs, fmt.Sprintf("order client: %v", err))
		} else {
			clients.Order = orderClient
		}
	}

	// 创建 Matching 客户端
	if cfg.MatchingGRPCAddr != "" {
		matchingClient, err := NewMatchingClient(cfg.MatchingGRPCAddr)
		if err != nil {
			errs = append(errs, fmt.Sprintf("matching client: %v", err))
		} else {
			clients.Matching = matchingClient
		}
	}

	if len(errs) > 0 {
		// 关闭已创建的连接
		clients.Close()
		return nil, fmt.Errorf("failed to create clients: %s", strings.Join(errs, "; "))
	}

	logger.Info("all gRPC clients initialized")
	return clients, nil
}

// Close 关闭所有连接
func (c *Clients) Close() error {
	var errs []string

	if c.User != nil {
		if err := c.User.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("user: %v", err))
		}
	}

	if c.Order != nil {
		if err := c.Order.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("order: %v", err))
		}
	}

	if c.Matching != nil {
		if err := c.Matching.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("matching: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing clients: %s", strings.Join(errs, "; "))
	}

	logger.Info("all gRPC clients closed")
	return nil
}
