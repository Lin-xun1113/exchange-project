// Package router 提供路由注册
package router

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/handler"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// Config 路由配置
type Config struct {
	JWTConfig   middleware.JWTConfig
	Clients     *client.Clients
	ServiceName string
}

// Setup 设置路由
func Setup(r *gin.Engine, cfg *Config) {
	// 全局中间件
	r.Use(middleware.RequestID())
	if cfg.ServiceName != "" {
		r.Use(otelgin.Middleware(cfg.ServiceName))
	}
	r.Use(middleware.Recovery())
	r.Use(middleware.AccessLog())
	r.Use(middleware.CORS())

	// 创建 Handler
	healthHandler := handler.NewHealthHandler()
	authHandler := handler.NewAuthHandler(cfg.Clients.User, cfg.JWTConfig.Secret, cfg.JWTConfig.ExpireTime)
	orderHandler := handler.NewOrderHandler(cfg.Clients)
	balanceHandler := handler.NewBalanceHandler(cfg.Clients)
	orderBookHandler := handler.NewOrderBookHandler(cfg.Clients)

	// 健康检查（不需要认证）
	r.GET("/healthz", healthHandler.Health)
	r.GET("/readyz", healthHandler.Ready)

	// Prometheus metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// API v1 路由组
	v1 := r.Group("/api/v1")
	{
		// 公开路由（不需要认证）
		public := v1.Group("")
		{
			// 认证
			public.POST("/auth/login", authHandler.Login)
			public.POST("/auth/register", authHandler.Register)
		}

		// 需要认证的路由
		auth := v1.Group("")
		auth.Use(middleware.JWT(cfg.JWTConfig))
		{
			// 订单相关
			orders := auth.Group("/orders")
			{
				orders.POST("", middleware.RequirePermission(middleware.PermissionOrderCreate), orderHandler.CreateOrder)
				orders.POST("/cancel", middleware.RequirePermission(middleware.PermissionOrderCancel), orderHandler.CancelOrder)
				orders.GET("", middleware.RequirePermission(middleware.PermissionOrderQuery), orderHandler.ListOrders)
				orders.GET("/:order_id", middleware.RequirePermission(middleware.PermissionOrderQuery), orderHandler.GetOrder)
			}

			// 余额相关
			balance := auth.Group("/balance")
			{
				balance.GET("", middleware.RequirePermission(middleware.PermissionBalanceQuery), balanceHandler.GetBalance)
			}
		}
	}

	// 订单簿（公开，但有频率限制）
	orderbook := r.Group("/orderbook")
	orderbook.Use(middleware.RateLimit(60, time.Minute))
	{
		orderbook.GET("/:symbol", orderBookHandler.GetOrderBook)
		orderbook.GET("", orderBookHandler.GetOrderBook)
	}
}

// NewRouter 创建 Gin 引擎
func NewRouter(cfg *config.Config, clients *client.Clients) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()

	// 设置路由
	Setup(r, &Config{
		JWTConfig: middleware.JWTConfig{
			Secret:     cfg.JWT.Secret,
			ExpireTime: cfg.JWT.GetExpireDuration(),
		},
		Clients:     clients,
		ServiceName: cfg.App.Name,
	})

	return r
}
