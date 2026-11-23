// Package router registers the gateway's HTTP routes.
package router

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/handler"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

func New(cfg *config.Config, cl *client.Clients, rdb redis.UniversalClient, m *metrics.Gateway) (*gin.Engine, error) {
	ipPolicy, ok := cfg.RateLimit.Policy("ip_global")
	if !ok {
		return nil, fmt.Errorf("missing rate limit policy %q", "ip_global")
	}
	writePolicy, ok := cfg.RateLimit.Policy("order_write")
	if !ok {
		return nil, fmt.Errorf("missing rate limit policy %q", "order_write")
	}

	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		return nil, err
	}
	r.Use(middleware.RequestID(), otelgin.Middleware("gateway"), middleware.Metrics(m),
		middleware.AccessLog(), middleware.Recovery(), middleware.CORS())

	// Health probes must remain independent of the Redis rate limiter.
	health := handler.NewHealth(cl.Conns, rdb)
	r.GET("/healthz", health.Live)
	r.GET("/readyz", health.Ready)
	writeLimit := func(c *gin.Context) { c.Next() }
	if cfg.RateLimit.Enabled {
		limiter := middleware.NewLimiter(rdb, m)
		r.Use(limiter.Middleware(ipPolicy))
		writeLimit = limiter.Middleware(writePolicy)
	}

	jwt := middleware.JWTConfig{Secret: cfg.JWT.Secret, ExpireTime: cfg.JWT.GetExpireDuration()}
	h := handler.New(cl, jwt, cfg.Symbols)
	v1 := r.Group("/api/v1")
	v1.POST("/auth/register", h.Register)
	v1.POST("/auth/login", h.Login)
	v1.GET("/market/orderbook/:symbol", h.OrderBook)
	v1.GET("/market/trades/:symbol", h.MarketTrades)
	v1.GET("/market/candles/:symbol", h.Candles)
	v1.GET("/market/tickers", h.Tickers)

	protected := v1.Group("", middleware.JWT(jwt))
	protected.POST("/account/deposit", middleware.RequirePermission(middleware.PermissionBalanceDeposit), writeLimit, h.Deposit)
	protected.GET("/account/balances", middleware.RequirePermission(middleware.PermissionBalanceQuery), h.Balances)
	protected.POST("/orders", middleware.RequirePermission(middleware.PermissionOrderCreate), writeLimit, h.CreateOrder)
	protected.DELETE("/orders/:order_id", middleware.RequirePermission(middleware.PermissionOrderCancel), writeLimit, h.CancelOrder)
	protected.GET("/orders/:order_id", middleware.RequirePermission(middleware.PermissionOrderQuery), h.GetOrder)
	protected.GET("/orders", middleware.RequirePermission(middleware.PermissionOrderQuery), h.ListOrders)
	protected.GET("/trades", middleware.RequirePermission(middleware.PermissionOrderQuery), h.ListTrades)
	return r, nil
}
