package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/internal/gateway/router"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const secret = "test-secret"

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		JWT:     config.JWTConfig{Secret: secret, ExpireTime: 1},
		Symbols: []string{"BTC/USDT"},
		RateLimit: config.RateLimitConfig{Enabled: true, Policies: []config.RateLimitPolicy{
			{Name: "ip_global", Scope: "ip", Limit: "100/s"},
			{Name: "order_write", Scope: "user", Limit: "1/s"},
		}},
	}
	for i := range cfg.RateLimit.Policies {
		require.NoError(t, cfg.RateLimit.Policies[i].ParseLimit())
	}
	return cfg
}

func TestHTTPMetricsUseTemplatesAndUnmatched(t *testing.T) {
	registry := prometheus.NewRegistry()
	cfg := testConfig(t)
	cfg.RateLimit.Enabled = false
	r, err := router.New(cfg, &client.Clients{}, nil, metrics.NewGateway(registry))
	require.NoError(t, err)
	for _, path := range []string{"/api/v1/orders/order-1", "/api/v1/orders/order-2", "/bad/first", "/bad/second", "/metrics"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	for _, method := range []string{"CUSTOM-ONE", "CUSTOM-TWO"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/api/v1/orders", nil))
	}
	families, err := registry.Gather()
	require.NoError(t, err)
	counts := make(map[string]float64)
	for _, family := range families {
		if family.GetName() != "http_requests_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string)
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			counts[labels["method"]+":"+labels["route"]+":"+labels["status"]] = metric.GetCounter().GetValue()
		}
	}
	require.Equal(t, map[string]float64{
		"GET:/api/v1/orders/:order_id:401": 2,
		"GET:unmatched:404":                3,
		"OTHER:unmatched:404":              2,
	}, counts)
}

func TestHealthRoutesBypassRateLimiter(t *testing.T) {
	srv := miniredis.RunT(t)
	srv.SetTime(time.Unix(1_700_000_000, 0))
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	cfg := testConfig(t)
	cfg.RateLimit.Policies[0].Limit = "1/s"
	require.NoError(t, cfg.RateLimit.Policies[0].ParseLimit())
	r, err := router.New(cfg, &client.Clients{}, rdb, metrics.NewGateway(prometheus.NewRegistry()))
	require.NoError(t, err)

	request := func(path string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code
	}
	require.Equal(t, http.StatusUnauthorized, request("/api/v1/orders"))
	require.Equal(t, http.StatusTooManyRequests, request("/api/v1/orders"))
	require.Equal(t, http.StatusOK, request("/healthz"))
	require.Equal(t, http.StatusServiceUnavailable, request("/readyz"))
}

type fakeOrder struct{ orderpb.OrderServiceClient }

func (fakeOrder) CreateOrder(context.Context, *orderpb.CreateOrderRequest, ...grpc.CallOption) (*orderpb.CreateOrderResponse, error) {
	return &orderpb.CreateOrderResponse{Order: &orderpb.Order{OrderId: "fake-order"}}, nil
}

func TestWriteLimitUsesJWTIdentityAfterPermission(t *testing.T) {
	srv := miniredis.RunT(t)
	srv.SetTime(time.Unix(1_700_000_000, 0))
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	cfg := testConfig(t)
	r, err := router.New(cfg, &client.Clients{Order: fakeOrder{}}, rdb, metrics.NewGateway(prometheus.NewRegistry()))
	require.NoError(t, err)

	create := func(id int64, role, addr string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", strings.NewReader(`{"symbol":"BTC/USDT","side":"buy","type":"limit","price":"20000","quantity":"1"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-1")
		req.RemoteAddr = addr
		if role != "" {
			token, _, err := middleware.GenerateToken(id, "testuser", role, secret, time.Hour)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	const ip1, ip2 = "192.0.2.10:1234", "192.0.2.20:1234"
	require.Equal(t, http.StatusUnauthorized, create(42, "", ip1))
	require.Equal(t, http.StatusForbidden, create(42, "viewer", ip1))
	require.Equal(t, http.StatusOK, create(42, "trader", ip1))
	require.Equal(t, http.StatusTooManyRequests, create(42, "trader", ip2))
	require.Equal(t, http.StatusOK, create(43, "trader", ip1))
}
