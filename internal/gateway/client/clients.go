// Package client connects the gateway to its downstream gRPC services.
package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	UserTarget     = "user-svc"
	OrderTarget    = "order-svc"
	MatchingTarget = "matching-svc"

	healthMethodPrefix = "/grpc.health.v1.Health/"
	defaultTimeout     = 3 * time.Second
)

// Clients holds the generated clients and the connections used for readiness checks.
type Clients struct {
	User     userpb.UserServiceClient
	Order    orderpb.OrderServiceClient
	Matching matchingpb.MatchingServiceClient
	Conns    map[string]*grpc.ClientConn
}

func New(cfg *config.Config, m *metrics.Gateway, reg prometheus.Registerer) (*Clients, error) {
	cl := &Clients{Conns: make(map[string]*grpc.ClientConn, 3)}
	observe := metrics.GRPCClientInterceptor(reg)
	retries := retryReads()
	for _, target := range [...]struct{ name, addr string }{
		{UserTarget, cfg.GRPC.UserGRPCAddr},
		{OrderTarget, cfg.GRPC.OrderGRPCAddr},
		{MatchingTarget, cfg.GRPC.MatchingGRPCAddr},
	} {
		conn, err := grpcx.Dial(target.addr, deadline, retries, breaker(target.name, m), observe)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("dial %s: %w", target.name, err), cl.Close())
		}
		cl.Conns[target.name] = conn
	}
	cl.User = userpb.NewUserServiceClient(cl.Conns[UserTarget])
	cl.Order = orderpb.NewOrderServiceClient(cl.Conns[OrderTarget])
	cl.Matching = matchingpb.NewMatchingServiceClient(cl.Conns[MatchingTarget])
	return cl, nil
}

func (c *Clients) Close() error {
	var errs []error
	for name, conn := range c.Conns {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func deadline(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	return invoke(ctx, method, req, reply, cc, opts...)
}

func retryReads() grpc.UnaryClientInterceptor {
	retryRead := retry.UnaryClientInterceptor(
		retry.WithMax(3),
		retry.WithCodes(codes.Unavailable),
		retry.WithPerRetryTimeout(time.Second),
	)
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		switch method {
		case userpb.UserService_GetUser_FullMethodName, userpb.UserService_GetBalances_FullMethodName,
			orderpb.OrderService_GetOrder_FullMethodName, orderpb.OrderService_ListOrders_FullMethodName, orderpb.OrderService_ListTrades_FullMethodName,
			orderpb.OrderService_ListMarketTrades_FullMethodName, orderpb.OrderService_ListCandles_FullMethodName, orderpb.OrderService_ListTickers_FullMethodName,
			matchingpb.MatchingService_GetOrderBook_FullMethodName:
			return retryRead(ctx, method, req, reply, cc, invoke, opts...)
		default:
			return invoke(ctx, method, req, reply, cc, opts...)
		}
	}
}

func breaker(target string, m *metrics.Gateway) grpc.UnaryClientInterceptor {
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        target,
		MaxRequests: 5,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= 5 },
		IsSuccessful: func(err error) bool {
			switch status.Code(err) {
			case codes.Unavailable, codes.DeadlineExceeded, codes.Internal:
				return false
			default:
				return true
			}
		},
		OnStateChange: func(_ string, _, to gobreaker.State) { m.BreakerState(target, int(to)) },
	})
	m.BreakerState(target, int(gobreaker.StateClosed))
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if strings.HasPrefix(method, healthMethodPrefix) {
			return invoke(ctx, method, req, reply, cc, opts...)
		}
		_, err := cb.Execute(func() (any, error) {
			return nil, invoke(ctx, method, req, reply, cc, opts...)
		})
		if err == gobreaker.ErrOpenState || err == gobreaker.ErrTooManyRequests {
			return status.Error(codes.Unavailable, "circuit open: "+target)
		}
		return err
	}
}
