package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRateLimitRefillAndFailOpen(t *testing.T) {
	srv := miniredis.RunT(t)
	at := time.Unix(1_700_000_000, 0)
	srv.SetTime(at)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	registry := prometheus.NewRegistry()
	m := metrics.NewGateway(registry)
	limiter := middleware.NewLimiter(rdb, m)

	policy := config.RateLimitPolicy{Name: "ip_global", Scope: "ip", Limit: "2/s"}
	require.NoError(t, policy.ParseLimit())
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/test", limiter.Middleware(policy), func(c *gin.Context) { c.Status(http.StatusOK) })

	request := func(remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = remoteAddr
		req.Header.Set("X-Forwarded-For", forwardedFor)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	const client = "192.0.2.10:4567"
	require.Equal(t, http.StatusOK, request(client, "").Code)
	require.Equal(t, http.StatusOK, request(client, "").Code)
	w := request(client, "203.0.113.11")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "1", w.Header().Get("Retry-After"))
	const key = "rl:ip_global:192.0.2.10"
	require.Equal(t, 2*time.Second, srv.TTL(key))

	// A fractional token must survive a denied request without allowing it early.
	srv.SetTime(at.Add(250 * time.Millisecond))
	require.Equal(t, http.StatusTooManyRequests, request(client, "").Code)
	srv.SetTime(at.Add(500 * time.Millisecond))
	require.Equal(t, http.StatusOK, request(client, "").Code)
	require.Equal(t, http.StatusTooManyRequests, request(client, "").Code)
	require.Equal(t, http.StatusOK, request("192.0.2.20:4567", "").Code)

	srv.Close()
	for range 6 {
		require.Equal(t, http.StatusOK, request(client, "").Code)
	}
	families, err := registry.Gather()
	require.NoError(t, err)
	counts := make(map[string]float64)
	for _, family := range families {
		if family.GetName() != "rate_limit_rejected_total" && family.GetName() != "rate_limit_errors_total" {
			continue
		}
		require.Len(t, family.Metric, 1)
		metric := family.Metric[0]
		require.Len(t, metric.Label, 1)
		require.Equal(t, "policy", metric.Label[0].GetName())
		require.Equal(t, policy.Name, metric.Label[0].GetValue())
		counts[family.GetName()] = metric.GetCounter().GetValue()
	}
	require.Equal(t, map[string]float64{"rate_limit_rejected_total": 3, "rate_limit_errors_total": 6}, counts)
}
