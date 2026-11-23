package client_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func startClients(t *testing.T, intercept grpc.UnaryServerInterceptor) (*client.Clients, *prometheus.Registry) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.UnaryInterceptor(intercept))
	userpb.RegisterUserServiceServer(server, userpb.UnimplementedUserServiceServer{})
	orderpb.RegisterOrderServiceServer(server, orderpb.UnimplementedOrderServiceServer{})
	matchingpb.RegisterMatchingServiceServer(server, matchingpb.UnimplementedMatchingServiceServer{})
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	reg := prometheus.NewRegistry()
	addr := listener.Addr().String()
	cfg := &config.Config{GRPC: config.GRPCConfig{UserGRPCAddr: addr, OrderGRPCAddr: addr, MatchingGRPCAddr: addr}}
	cl, err := client.New(cfg, metrics.NewGateway(reg), reg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cl.Close()) })
	return cl, reg
}

func TestClientRetriesOnlyExplicitReads(t *testing.T) {
	for _, tc := range []struct {
		name, target, method string
		req, resp            proto.Message
		wantCalls            int
	}{
		{"get user", "user-svc", userpb.UserService_GetUser_FullMethodName, &userpb.GetUserRequest{}, &userpb.GetUserResponse{}, 2},
		{"get balances", "user-svc", userpb.UserService_GetBalances_FullMethodName, &userpb.GetBalancesRequest{}, &userpb.GetBalancesResponse{}, 2},
		{"get order", "order-svc", orderpb.OrderService_GetOrder_FullMethodName, &orderpb.GetOrderRequest{}, &orderpb.GetOrderResponse{}, 2},
		{"list orders", "order-svc", orderpb.OrderService_ListOrders_FullMethodName, &orderpb.ListOrdersRequest{}, &orderpb.ListOrdersResponse{}, 2},
		{"list trades", "order-svc", orderpb.OrderService_ListTrades_FullMethodName, &orderpb.ListTradesRequest{}, &orderpb.ListTradesResponse{}, 2},
		{"get order book", "matching-svc", matchingpb.MatchingService_GetOrderBook_FullMethodName, &matchingpb.GetOrderBookRequest{}, &matchingpb.GetOrderBookResponse{}, 2},
		{"login", "user-svc", userpb.UserService_Login_FullMethodName, &userpb.LoginRequest{}, &userpb.LoginResponse{}, 1},
		{"create user", "user-svc", userpb.UserService_CreateUser_FullMethodName, &userpb.CreateUserRequest{}, &userpb.CreateUserResponse{}, 1},
		{"deposit", "user-svc", userpb.UserService_Deposit_FullMethodName, &userpb.DepositRequest{}, &userpb.DepositResponse{}, 1},
		{"create order", "order-svc", orderpb.OrderService_CreateOrder_FullMethodName, &orderpb.CreateOrderRequest{}, &orderpb.CreateOrderResponse{}, 1},
		{"cancel order", "order-svc", orderpb.OrderService_CancelOrder_FullMethodName, &orderpb.CancelOrderRequest{}, &orderpb.CancelOrderResponse{}, 1},
		{"health", "matching-svc", healthpb.Health_Check_FullMethodName, &healthpb.HealthCheckRequest{}, &healthpb.HealthCheckResponse{}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			cl, _ := startClients(t, func(ctx context.Context, req any, info *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				if info.FullMethod != tc.method || len(metadata.ValueFromIncomingContext(ctx, grpcx.RequestIDMetadataKey)) != 1 ||
					metadata.ValueFromIncomingContext(ctx, grpcx.RequestIDMetadataKey)[0] != "test-request" {
					return nil, status.Error(codes.Internal, "request metadata or method not propagated")
				}
				if calls == 1 {
					return nil, status.Error(codes.Unavailable, "upstream unavailable")
				}
				// The reply is empty but uses the method's real response message type.
				return tc.resp, nil
			})
			ctx, cancel := context.WithTimeout(logger.WithRequestID(context.Background(), "test-request"), 2*time.Second)
			defer cancel()
			require.Contains(t, cl.Conns, tc.target)
			// Use a distinct receive buffer to avoid sharing protobuf state with the server test double.
			reply := tc.resp.ProtoReflect().New().Interface()
			err := cl.Conns[tc.target].Invoke(ctx, tc.method, tc.req, reply)
			if tc.wantCalls > 1 {
				require.NoError(t, err)
			} else {
				require.Equal(t, codes.Unavailable, status.Code(err), err)
			}
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestCircuitBreakerIgnoresDenialsAndHealthBypassesOpenCircuit(t *testing.T) {
	var mu sync.Mutex
	orderCalls := 0
	cl, reg := startClients(t, func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod == healthpb.Health_Check_FullMethodName {
			return handler(ctx, req)
		}
		mu.Lock()
		defer mu.Unlock()
		orderCalls++
		if orderCalls <= 6 {
			return nil, status.Error(codes.InvalidArgument, "order denied")
		}
		return nil, status.Error(codes.Unavailable, "upstream unavailable")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for range 6 {
		_, err := cl.Order.CreateOrder(ctx, &orderpb.CreateOrderRequest{})
		require.Equal(t, codes.InvalidArgument, status.Code(err), err)
	}
	for range 5 {
		_, err := cl.Order.CreateOrder(ctx, &orderpb.CreateOrderRequest{})
		require.Equal(t, "upstream unavailable", status.Convert(err).Message())
	}
	_, err := cl.Order.CreateOrder(ctx, &orderpb.CreateOrderRequest{})
	require.Equal(t, codes.Unavailable, status.Code(err), err)
	require.Equal(t, "circuit open: order-svc", status.Convert(err).Message())
	resp, err := healthpb.NewHealthClient(cl.Conns["order-svc"]).Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())
	mu.Lock()
	calls := orderCalls
	mu.Unlock()
	require.Equal(t, 11, calls)

	families, err := reg.Gather()
	require.NoError(t, err)
	states := make(map[string]float64)
	for _, family := range families {
		if family.GetName() != "grpc_client_breaker_state" {
			continue
		}
		for _, metric := range family.Metric {
			require.Len(t, metric.Label, 1)
			require.Equal(t, "target", metric.Label[0].GetName())
			states[metric.Label[0].GetValue()] = metric.GetGauge().GetValue()
		}
	}
	require.Equal(t, map[string]float64{"user-svc": 0, "order-svc": 2, "matching-svc": 0}, states)
}
