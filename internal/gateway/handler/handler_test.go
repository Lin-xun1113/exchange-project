package handler_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/internal/gateway/router"
	"github.com/linxun2025/exchange-project/pkg/config"
	apperrors "github.com/linxun2025/exchange-project/pkg/errors"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	secret = "test-secret"
	userID = int64(42)
)

type fakeUser struct {
	userpb.UserServiceClient
	login    func(*userpb.LoginRequest) (*userpb.LoginResponse, error)
	register func(*userpb.CreateUserRequest) (*userpb.CreateUserResponse, error)
	deposit  func(*userpb.DepositRequest) (*userpb.DepositResponse, error)
	balances func(*userpb.GetBalancesRequest) (*userpb.GetBalancesResponse, error)
}

func (f fakeUser) Login(_ context.Context, req *userpb.LoginRequest, _ ...grpc.CallOption) (*userpb.LoginResponse, error) {
	return f.login(req)
}

func (f fakeUser) CreateUser(_ context.Context, req *userpb.CreateUserRequest, _ ...grpc.CallOption) (*userpb.CreateUserResponse, error) {
	return f.register(req)
}

func (f fakeUser) Deposit(_ context.Context, req *userpb.DepositRequest, _ ...grpc.CallOption) (*userpb.DepositResponse, error) {
	return f.deposit(req)
}

func (f fakeUser) GetBalances(_ context.Context, req *userpb.GetBalancesRequest, _ ...grpc.CallOption) (*userpb.GetBalancesResponse, error) {
	return f.balances(req)
}

type fakeOrder struct {
	orderpb.OrderServiceClient
	create func(*orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error)
	get    func(*orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error)
	cancel func(*orderpb.CancelOrderRequest) (*orderpb.CancelOrderResponse, error)
	list   func(*orderpb.ListOrdersRequest) (*orderpb.ListOrdersResponse, error)
	trades func(*orderpb.ListTradesRequest) (*orderpb.ListTradesResponse, error)
}

func (f fakeOrder) CreateOrder(_ context.Context, req *orderpb.CreateOrderRequest, _ ...grpc.CallOption) (*orderpb.CreateOrderResponse, error) {
	return f.create(req)
}

func (f fakeOrder) GetOrder(_ context.Context, req *orderpb.GetOrderRequest, _ ...grpc.CallOption) (*orderpb.GetOrderResponse, error) {
	return f.get(req)
}

func (f fakeOrder) CancelOrder(_ context.Context, req *orderpb.CancelOrderRequest, _ ...grpc.CallOption) (*orderpb.CancelOrderResponse, error) {
	return f.cancel(req)
}

func (f fakeOrder) ListOrders(_ context.Context, req *orderpb.ListOrdersRequest, _ ...grpc.CallOption) (*orderpb.ListOrdersResponse, error) {
	return f.list(req)
}

func (f fakeOrder) ListTrades(_ context.Context, req *orderpb.ListTradesRequest, _ ...grpc.CallOption) (*orderpb.ListTradesResponse, error) {
	return f.trades(req)
}

type fakeMatching struct {
	matchingpb.MatchingServiceClient
	book func(*matchingpb.GetOrderBookRequest) (*matchingpb.GetOrderBookResponse, error)
}

func (f fakeMatching) GetOrderBook(_ context.Context, req *matchingpb.GetOrderBookRequest, _ ...grpc.CallOption) (*matchingpb.GetOrderBookResponse, error) {
	return f.book(req)
}

func testRouter(t *testing.T, cl *client.Clients, rdb redis.UniversalClient) *gin.Engine {
	t.Helper()
	cfg := &config.Config{
		JWT:     config.JWTConfig{Secret: secret, ExpireTime: 24},
		Symbols: []string{"BTC/USDT"},
		RateLimit: config.RateLimitConfig{Policies: []config.RateLimitPolicy{
			{Name: "ip_global", Scope: "ip", MaxCount: 100, WindowSec: 1},
			{Name: "order_write", Scope: "user", MaxCount: 10, WindowSec: 1},
		}},
	}
	r, err := router.New(cfg, cl, rdb, metrics.NewGateway(prometheus.NewRegistry()))
	require.NoError(t, err)
	return r
}

func serve(t *testing.T, r http.Handler, method, path, body, key, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if role != "" {
		token, _, err := middleware.GenerateToken(userID, "testuser", role, secret, time.Hour)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decode(t *testing.T, w *httptest.ResponseRecorder, statusCode int) envelope {
	t.Helper()
	require.Equal(t, statusCode, w.Code, w.Body.String())
	var msg envelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msg), w.Body.String())
	return msg
}

func TestHealthHandler(t *testing.T) {
	r := testRouter(t, &client.Clients{}, nil)
	w := serve(t, r, http.MethodGet, "/healthz", "", "", "")
	msg := decode(t, w, http.StatusOK)
	require.Zero(t, msg.Code)
	var data struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(msg.Data, &data))
	require.Equal(t, "ok", data.Status)
}

func TestAuthHandlerLogin(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		upstream   error
		status     int
	}{
		{"success", `{"username":"testuser","password":"password123"}`, nil, http.StatusOK},
		{"invalid request", "", nil, http.StatusBadRequest},
		{"authentication failure", `{"username":"testuser","password":"wrongpassword"}`, status.Error(codes.Unauthenticated, "invalid credentials"), http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r := testRouter(t, &client.Clients{User: fakeUser{login: func(req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
				calls++
				require.Equal(t, "testuser", req.GetUsername())
				if tc.upstream != nil {
					return nil, tc.upstream
				}
				require.Equal(t, "password123", req.GetPassword())
				return &userpb.LoginResponse{UserId: userID, Username: "testuser", Role: "trader"}, nil
			}}}, nil)
			w := serve(t, r, http.MethodPost, "/api/v1/auth/login", tc.body, "", "")
			msg := decode(t, w, tc.status)
			switch tc.status {
			case http.StatusOK:
				require.Equal(t, 1, calls)
				require.Zero(t, msg.Code)
				var data struct {
					Token     string `json:"token"`
					ExpiresAt int64  `json:"expires_at"`
					UserID    int64  `json:"user_id"`
					Username  string `json:"username"`
					Role      string `json:"role"`
				}
				require.NoError(t, json.Unmarshal(msg.Data, &data))
				claims, err := middleware.ParseToken(data.Token, secret)
				require.NoError(t, err)
				require.Equal(t, userID, claims.UserID)
				require.Equal(t, "testuser", claims.Username)
				require.Equal(t, "trader", claims.Role)
				require.Equal(t, claims.ExpiresAt.Unix(), data.ExpiresAt)
				require.Equal(t, claims.UserID, data.UserID)
				require.Equal(t, claims.Username, data.Username)
				require.Equal(t, claims.Role, data.Role)
			case http.StatusBadRequest:
				require.Zero(t, calls)
			case http.StatusUnauthorized:
				require.Equal(t, 1, calls)
				require.Equal(t, "invalid credentials", msg.Message)
			}
		})
	}
}

func TestRegister(t *testing.T) {
	r := testRouter(t, &client.Clients{User: fakeUser{register: func(req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
		require.Equal(t, "testuser", req.GetUsername())
		require.Equal(t, "password123", req.GetPassword())
		require.Equal(t, "testuser@example.com", req.GetEmail())
		return &userpb.CreateUserResponse{User: &userpb.User{Id: userID, Username: "testuser"}}, nil
	}}}, nil)
	w := serve(t, r, http.MethodPost, "/api/v1/auth/register", `{"username":"testuser","password":"password123","email":"testuser@example.com"}`, "", "")
	require.JSONEq(t, `{"user_id":42,"username":"testuser"}`, string(decode(t, w, http.StatusOK).Data))
}

func TestJWTMiddleware(t *testing.T) {
	for _, tc := range []struct {
		name, auth string
		status     int
	}{
		{"valid", "valid", http.StatusOK},
		{"invalid", "Bearer invalid-token", http.StatusUnauthorized},
		{"missing", "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/protected", middleware.JWT(middleware.JWTConfig{Secret: secret}), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"user_id": middleware.GetUserID(c), "username": middleware.GetUsername(c), "role": middleware.GetRole(c)})
			})
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tc.auth == "valid" {
				token, _, err := middleware.GenerateToken(userID, "testuser", "trader", secret, time.Hour)
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+token)
			} else if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusOK {
				require.JSONEq(t, `{"user_id":42,"username":"testuser","role":"trader"}`, w.Body.String())
			}
		})
	}
}

const validOrder = `{"user_id":999,"symbol":"btc-usdt","side":"buy","type":"limit","price":"20000.00000001","quantity":"0.01"}`

func TestCreateOrderRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name, body, key string
	}{
		{"missing key", validOrder, ""},
		{"malformed key", validOrder, "bad key"},
		{"JSON number price", `{"symbol":"BTC/USDT","side":"buy","type":"limit","price":20000.00000001,"quantity":"0.01"}`, "order-1"},
		{"JSON number quantity", `{"symbol":"BTC/USDT","side":"buy","type":"limit","price":"20000","quantity":0.01}`, "order-1"},
		{"unknown symbol", `{"symbol":"ETH/USDT","side":"buy","type":"limit","price":"20000","quantity":"0.01"}`, "order-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r := testRouter(t, &client.Clients{Order: fakeOrder{create: func(*orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
				calls++
				return nil, status.Error(codes.Internal, "unexpected order call")
			}}}, nil)
			w := serve(t, r, http.MethodPost, "/api/v1/orders", tc.body, tc.key, "trader")
			require.Equal(t, apperrors.CodeInvalidParam, decode(t, w, http.StatusBadRequest).Code)
			require.Zero(t, calls)
		})
	}
}

func TestCreateOrderUsesJWTUserAndIdempotencyKey(t *testing.T) {
	const key = "Order_1-pending"
	var received *orderpb.CreateOrderRequest
	r := testRouter(t, &client.Clients{Order: fakeOrder{create: func(req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
		received = req
		at := timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		return &orderpb.CreateOrderResponse{Order: &orderpb.Order{
			OrderId: "order-1", UserId: req.GetUserId(), Symbol: req.GetSymbol(), Side: req.GetSide(), Type: req.GetType(),
			Price: req.GetPrice(), Quantity: req.GetQuantity(), FilledQuantity: "0", Status: orderpb.OrderStatus_ORDER_STATUS_PENDING,
			CreatedAt: at, UpdatedAt: at,
		}}, nil
	}}}, nil)
	w := serve(t, r, http.MethodPost, "/api/v1/orders", validOrder, key, "trader")
	msg := decode(t, w, http.StatusOK)
	require.Zero(t, msg.Code)
	require.NotNil(t, received)
	require.Equal(t, userID, received.GetUserId())
	require.Equal(t, key, received.GetIdempotencyKey())
	require.Equal(t, "BTC/USDT", received.GetSymbol())
	require.Equal(t, "20000.00000001", received.GetPrice())
	require.Equal(t, "0.01", received.GetQuantity())
	require.JSONEq(t, `{"order":{"order_id":"order-1","symbol":"BTC/USDT","side":"buy","type":"limit","price":"20000.00000001","quantity":"0.01","filled_quantity":"0","status":"pending","reject_reason":"","cancel_requested":false,"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"},"replayed":false}`, string(msg.Data))
}

func TestOrderRPCError(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		http int
		app  int
		msg  string
	}{
		{codes.InvalidArgument, 400, apperrors.CodeInvalidParam, "upstream message"},
		{codes.Unauthenticated, 401, apperrors.CodeUnauthorized, "upstream message"},
		{codes.PermissionDenied, 403, apperrors.CodeForbidden, "upstream message"},
		{codes.NotFound, 404, apperrors.CodeNotFound, "upstream message"},
		{codes.AlreadyExists, 409, apperrors.CodeConflict, "upstream message"},
		{codes.FailedPrecondition, 409, apperrors.CodeConflict, "upstream message"},
		{codes.Unavailable, 503, apperrors.CodeServiceUnavailable, "service unavailable"},
		{codes.ResourceExhausted, 503, apperrors.CodeServiceUnavailable, "service unavailable"},
		{codes.DeadlineExceeded, 504, apperrors.CodeServiceUnavailable, "upstream timeout"},
		{codes.Internal, 500, apperrors.CodeInternalError, "internal server error"},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			r := testRouter(t, &client.Clients{Order: fakeOrder{get: func(req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
				require.Equal(t, userID, req.GetUserId())
				require.Equal(t, "order-1", req.GetOrderId())
				return nil, status.Error(tc.code, "upstream message")
			}}}, nil)
			w := serve(t, r, http.MethodGet, "/api/v1/orders/order-1", "", "", "viewer")
			msg := decode(t, w, tc.http)
			require.Equal(t, tc.app, msg.Code)
			require.Equal(t, tc.msg, msg.Message)
		})
	}
}

func TestAccountRoutes(t *testing.T) {
	calls := 0
	balance := &userpb.Balance{Asset: "USDT", Available: "10.00000001", Frozen: "0"}
	r := testRouter(t, &client.Clients{User: fakeUser{
		deposit: func(req *userpb.DepositRequest) (*userpb.DepositResponse, error) {
			calls++
			require.Equal(t, userID, req.GetUserId())
			require.Equal(t, "USDT", req.GetAsset())
			require.Equal(t, "10.00000001", req.GetAmount())
			require.Equal(t, "42:account-1", req.GetRefId())
			return &userpb.DepositResponse{Balance: balance}, nil
		},
		balances: func(req *userpb.GetBalancesRequest) (*userpb.GetBalancesResponse, error) {
			require.Equal(t, userID, req.GetUserId())
			return &userpb.GetBalancesResponse{Balances: []*userpb.Balance{balance}}, nil
		},
	}}, nil)
	const path = "/api/v1/account/deposit"
	const body = `{"asset":"USDT","amount":"10.00000001"}`
	w := serve(t, r, http.MethodPost, path, body, "account-1", "viewer")
	require.Equal(t, apperrors.CodeForbidden, decode(t, w, http.StatusForbidden).Code)
	w = serve(t, r, http.MethodPost, path, body, "", "trader")
	decode(t, w, http.StatusBadRequest)
	w = serve(t, r, http.MethodPost, path, `{"asset":"USDT","amount":10.00000001}`, "account-1", "trader")
	decode(t, w, http.StatusBadRequest)
	require.Zero(t, calls)

	w = serve(t, r, http.MethodPost, path, body, "account-1", "trader")
	const expectedBalance = `{"asset":"USDT","available":"10.00000001","frozen":"0"}`
	require.JSONEq(t, expectedBalance, string(decode(t, w, http.StatusOK).Data))
	require.Equal(t, 1, calls)
	w = serve(t, r, http.MethodGet, "/api/v1/account/balances", "", "", "viewer")
	require.JSONEq(t, "["+expectedBalance+"]", string(decode(t, w, http.StatusOK).Data))
}

func TestCancelAndListRoutes(t *testing.T) {
	r := testRouter(t, &client.Clients{Order: fakeOrder{
		cancel: func(req *orderpb.CancelOrderRequest) (*orderpb.CancelOrderResponse, error) {
			require.Equal(t, userID, req.GetUserId())
			require.Equal(t, "order-1", req.GetOrderId())
			return &orderpb.CancelOrderResponse{Order: &orderpb.Order{OrderId: "order-1", Status: orderpb.OrderStatus_ORDER_STATUS_OPEN, CancelRequested: true}}, nil
		},
		list: func(req *orderpb.ListOrdersRequest) (*orderpb.ListOrdersResponse, error) {
			require.Equal(t, userID, req.GetUserId())
			require.Equal(t, "BTC/USDT", req.GetSymbol())
			require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED, req.GetStatus())
			require.Equal(t, int32(3), req.GetPageSize())
			require.Equal(t, "abc=", req.GetPageToken())
			return &orderpb.ListOrdersResponse{NextPageToken: "next-page="}, nil
		},
		trades: func(req *orderpb.ListTradesRequest) (*orderpb.ListTradesResponse, error) {
			require.Equal(t, userID, req.GetUserId())
			require.Empty(t, req.GetSymbol())
			require.Equal(t, int32(50), req.GetPageSize())
			return &orderpb.ListTradesResponse{Trades: []*orderpb.Trade{{
				TradeId: "trade-1", Symbol: "BTC/USDT", OrderId: "order-1", Side: orderpb.OrderSide_ORDER_SIDE_BUY,
				Price: "12.3", Quantity: "0.01", Maker: true, CreatedAt: timestamppb.New(time.Unix(0, 0)),
			}}}, nil
		},
	}}, nil)
	w := serve(t, r, http.MethodDelete, "/api/v1/orders/order-1", "", "", "trader")
	msg := decode(t, w, http.StatusOK)
	var cancelled struct {
		Order struct {
			ID              string `json:"order_id"`
			CancelRequested bool   `json:"cancel_requested"`
		} `json:"order"`
	}
	require.NoError(t, json.Unmarshal(msg.Data, &cancelled))
	require.Equal(t, "order-1", cancelled.Order.ID)
	require.True(t, cancelled.Order.CancelRequested)

	w = serve(t, r, http.MethodGet, "/api/v1/orders?symbol=btc-usdt&status=partial_filled&page_size=3&page_token=abc%3D", "", "", "viewer")
	require.JSONEq(t, `{"orders":[],"next_page_token":"next-page="}`, string(decode(t, w, http.StatusOK).Data))
	w = serve(t, r, http.MethodGet, "/api/v1/trades", "", "", "viewer")
	require.JSONEq(t, `{"trades":[{"trade_id":"trade-1","symbol":"BTC/USDT","order_id":"order-1","side":"buy","price":"12.3","quantity":"0.01","maker":true,"created_at":"1970-01-01T00:00:00Z"}],"next_page_token":""}`, string(decode(t, w, http.StatusOK).Data))
}

func TestOrderBookIsPublic(t *testing.T) {
	r := testRouter(t, &client.Clients{Matching: fakeMatching{book: func(req *matchingpb.GetOrderBookRequest) (*matchingpb.GetOrderBookResponse, error) {
		require.Equal(t, "BTC/USDT", req.GetSymbol())
		require.Equal(t, int32(20), req.GetDepth())
		return &matchingpb.GetOrderBookResponse{Symbol: "BTC/USDT", Lsn: 7, Asks: []*matchingpb.PriceLevel{{Price: "20000", Quantity: "0.2", OrderCount: 1}}}, nil
	}}}, nil)
	const path = "/api/v1/market/orderbook/BTC-USDT"
	for _, query := range []string{"", "?depth=0"} {
		w := serve(t, r, http.MethodGet, path+query, "", "", "")
		require.JSONEq(t, `{"symbol":"BTC/USDT","bids":[],"asks":[{"price":"20000","quantity":"0.2","orders":1}],"lsn":7}`, string(decode(t, w, http.StatusOK).Data), "query %q", query)
	}
	for _, query := range []string{"?depth=-1", "?depth=201"} {
		w := serve(t, r, http.MethodGet, path+query, "", "", "")
		require.Equal(t, http.StatusBadRequest, w.Code, "query %q", query)
	}
}

func startHealthServer(t *testing.T) (*grpc.ClientConn, *health.Server) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	healthService := health.NewServer()
	healthpb.RegisterHealthServer(server, healthService)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///"+listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}, grpc.WaitForReady(true))
	require.NoError(t, err)
	return conn, healthService
}

func TestReadinessChecksRealGRPCHealthAndRedis(t *testing.T) {
	conns := make(map[string]*grpc.ClientConn)
	services := make(map[string]*health.Server)
	for _, name := range []string{"user-svc", "order-svc", "matching-svc"} {
		conns[name], services[name] = startHealthServer(t)
	}
	redisServer := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: redisServer.Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	r := testRouter(t, &client.Clients{Conns: conns}, rdb)

	ready := func(statusCode int, expected string) {
		t.Helper()
		w := serve(t, r, http.MethodGet, "/readyz", "", "", "")
		require.Equal(t, statusCode, w.Code, w.Body.String())
		require.JSONEq(t, expected, w.Body.String())
	}
	ready(http.StatusOK, `{"status":"ready"}`)
	services["order-svc"].SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	ready(http.StatusServiceUnavailable, `{"status":"not_ready","failed":["order-svc"]}`)
	services["matching-svc"].SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	redisServer.Close()
	ready(http.StatusServiceUnavailable, `{"status":"not_ready","failed":["matching-svc","order-svc","redis"]}`)

	w := serve(t, r, http.MethodGet, "/healthz", "", "", "")
	require.Equal(t, http.StatusOK, w.Code, "liveness with failed dependencies: %s", w.Body)
}
