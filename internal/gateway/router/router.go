// Package router provides routing registration
package router

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/handler"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/internal/order/outbox"
	"github.com/linxun2025/exchange-project/internal/order/saga"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/logger"
	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"gorm.io/gorm"
)

// Config 路由配置
type Config struct {
	JWTConfig     middleware.JWTConfig
	Clients       *client.Clients
	ServiceName   string
	RedisClient   *redis.Client
	RateLimitCfg  *config.RateLimitConfig
	DB            *gorm.DB
	OutboxWorker  *outbox.Worker
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

	// Rate limiting middleware (Redis-based distributed rate limiting)
	if cfg.RedisClient != nil && cfg.RateLimitCfg != nil && cfg.RateLimitCfg.Enabled {
		r.Use(middleware.RateLimitMiddleware(cfg.RedisClient, cfg.RateLimitCfg))
	}

	// 创建 Handler
	healthHandler := handler.NewHealthHandler()
	authHandler := handler.NewAuthHandler(cfg.Clients.User, cfg.JWTConfig.Secret, cfg.JWTConfig.ExpireTime)

	// 使用带 Saga 支持的 OrderHandler
	var orderHandler *handler.OrderHandler
	if cfg.DB != nil && cfg.OutboxWorker != nil {
		orderHandler = handler.NewOrderHandlerWithSaga(cfg.Clients, cfg.DB, cfg.OutboxWorker)
	} else {
		orderHandler = handler.NewOrderHandler(cfg.Clients)
	}

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
	// 注意: 现在使用全局 Redis 限流，此处保留旧的内存限流作为备用
	orderbook := r.Group("/orderbook")
	{
		orderbook.GET("/:symbol", orderBookHandler.GetOrderBook)
		orderbook.GET("", orderBookHandler.GetOrderBook)
	}
}

// NewRouter creates Gin engine with saga support
func NewRouter(cfg *config.Config, clients *client.Clients, redisClient *redis.Client, db *gorm.DB) (*gin.Engine, *outbox.Worker) {
	gin.SetMode(gin.ReleaseMode)

	// Parse rate limit policies
	for i := range cfg.RateLimit.Policies {
		if err := cfg.RateLimit.Policies[i].ParseLimit(); err != nil {
			logger.Warn("failed to parse rate limit policy",
				logger.S("policy", cfg.RateLimit.Policies[i].Name),
				logger.Err(err),
			)
		}
	}

	// Create outbox repository and handlers
	outboxRepo := outbox.NewGormRepository(db)
	orderRepo := handler.NewGatewayOrderRepository(db)
	sagaOrchestrator := saga.NewOrderSaga(db, orderRepo, outboxRepo)

	// Create delivery handlers
	outboxHandlers := outbox.ActionHandlers{
		outbox.ActionFreezeBalance:   createFreezeBalanceHandler(clients.User),
		outbox.ActionUnfreezeBalance: createUnfreezeBalanceHandler(clients.User),
		outbox.ActionSubmitMatching:  createSubmitMatchingHandler(clients.Matching),
		outbox.ActionUpdateStatus:    createUpdateStatusHandler(orderRepo),
	}

	// Create outbox worker
	outboxWorker := outbox.NewWorker(outboxRepo, outboxHandlers, outbox.DefaultWorkerConfig())

	// Recover stale entries from previous crashes
	if err := outboxWorker.RecoverStaleEntries(context.Background(), 30*time.Second); err != nil {
		logger.Error("failed to recover stale outbox entries", logger.Err(err))
	}

	// Register saga callbacks
	outboxWorker.RegisterCallback(outbox.ActionFreezeBalance, createFreezeSuccessCallback(sagaOrchestrator))
	outboxWorker.RegisterCallback(outbox.ActionUnfreezeBalance, createUnfreezeSuccessCallback(sagaOrchestrator))
	outboxWorker.RegisterCallback(outbox.ActionSubmitMatching, createSubmitMatchingSuccessCallback(sagaOrchestrator))

	r := gin.New()

	// Setup routes
	Setup(r, &Config{
		JWTConfig: middleware.JWTConfig{
			Secret:     cfg.JWT.Secret,
			ExpireTime: cfg.JWT.GetExpireDuration(),
		},
		Clients:      clients,
		ServiceName:  cfg.App.Name,
		RedisClient:  redisClient,
		RateLimitCfg: &cfg.RateLimit,
		DB:           db,
		OutboxWorker: outboxWorker,
	})

	return r, outboxWorker
}

// createFreezeBalanceHandler creates handler for freeze balance action
func createFreezeBalanceHandler(userClient *client.UserClient) outbox.DeliveryHandler {
	return func(ctx context.Context, entry *outbox.OutboxEntry) error {
		var payload outbox.FreezeBalancePayload
		if err := json.Unmarshal([]byte(entry.Payload), &payload); err != nil {
			return fmt.Errorf("failed to unmarshal payload: %w", err)
		}

		resp, err := userClient.FreezeAmount(ctx, &userpb.FreezeAmountRequest{
			UserId:  payload.UserID,
			Amount:  fmt.Sprintf("%.8f", payload.Amount),
			OrderId: payload.OrderID,
		})
		if err != nil {
			return fmt.Errorf("failed to freeze balance: %w", err)
		}
		if !resp.Success {
			return fmt.Errorf("freeze balance failed: insufficient balance")
		}
		return nil
	}
}

// createUnfreezeBalanceHandler creates handler for unfreeze balance action
func createUnfreezeBalanceHandler(userClient *client.UserClient) outbox.DeliveryHandler {
	return func(ctx context.Context, entry *outbox.OutboxEntry) error {
		var payload outbox.UnfreezeBalancePayload
		if err := json.Unmarshal([]byte(entry.Payload), &payload); err != nil {
			return fmt.Errorf("failed to unmarshal payload: %w", err)
		}

		resp, err := userClient.UnfreezeAmount(ctx, &userpb.UnfreezeAmountRequest{
			UserId:  payload.UserID,
			Amount:  fmt.Sprintf("%.8f", payload.Amount),
			OrderId: payload.OrderID,
		})
		if err != nil {
			return fmt.Errorf("failed to unfreeze balance: %w", err)
		}
		if !resp.Success {
			return fmt.Errorf("unfreeze balance failed")
		}
		return nil
	}
}

// createSubmitMatchingHandler creates handler for submit matching action
func createSubmitMatchingHandler(matchingClient *client.MatchingClient) outbox.DeliveryHandler {
	return func(ctx context.Context, entry *outbox.OutboxEntry) error {
		var payload outbox.SubmitMatchingPayload
		if err := json.Unmarshal([]byte(entry.Payload), &payload); err != nil {
			return fmt.Errorf("failed to unmarshal payload: %w", err)
		}

		_, err := matchingClient.SubmitOrder(ctx, &matchingpb.SubmitOrderRequest{
			UserId:    payload.UserID,
			Symbol:    payload.Symbol,
			Side:      matchingpb.OrderSide(matchingpb.OrderSide_value["ORDER_SIDE_"+strings.ToUpper(payload.Side)]),
			OrderType: matchingpb.OrderType(matchingpb.OrderType_value["ORDER_TYPE_"+strings.ToUpper(payload.OrderType)]),
			Price:     payload.Price,
			Quantity:  payload.Quantity,
		})
		if err != nil {
			return fmt.Errorf("failed to submit matching: %w", err)
		}
		return nil
	}
}

// createFreezeSuccessCallback creates callback for freeze balance success
func createFreezeSuccessCallback(sagaOrchestrator *saga.OrderSaga) outbox.SuccessCallback {
	return func(ctx context.Context, entry *outbox.OutboxEntry) error {
		var payload outbox.FreezeBalancePayload
		if err := json.Unmarshal([]byte(entry.Payload), &payload); err != nil {
			return fmt.Errorf("failed to unmarshal payload: %w", err)
		}

		return sagaOrchestrator.HandleFreezeBalanceSuccess(ctx, entry.SagaID, payload.OrderID, &payload)
	}
}

// createUnfreezeSuccessCallback creates callback for unfreeze balance success
func createUnfreezeSuccessCallback(sagaOrchestrator *saga.OrderSaga) outbox.SuccessCallback {
	return func(ctx context.Context, entry *outbox.OutboxEntry) error {
		var payload outbox.UnfreezeBalancePayload
		if err := json.Unmarshal([]byte(entry.Payload), &payload); err != nil {
			return fmt.Errorf("failed to unmarshal payload: %w", err)
		}

		return sagaOrchestrator.HandleUnfreezeSuccess(ctx, entry.SagaID, payload.OrderID)
	}
}

// createSubmitMatchingSuccessCallback creates callback for submit matching success
func createSubmitMatchingSuccessCallback(sagaOrchestrator *saga.OrderSaga) outbox.SuccessCallback {
	return func(ctx context.Context, entry *outbox.OutboxEntry) error {
		var payload outbox.SubmitMatchingPayload
		if err := json.Unmarshal([]byte(entry.Payload), &payload); err != nil {
			return fmt.Errorf("failed to unmarshal payload: %w", err)
		}

		return sagaOrchestrator.HandleSubmitMatchingSuccess(ctx, entry.SagaID, payload.OrderID)
	}
}

// createUpdateStatusHandler creates handler for update status action
func createUpdateStatusHandler(orderRepo *handler.GatewayOrderRepository) outbox.DeliveryHandler {
	return func(ctx context.Context, entry *outbox.OutboxEntry) error {
		var payload outbox.UpdateStatusPayload
		if err := json.Unmarshal([]byte(entry.Payload), &payload); err != nil {
			return fmt.Errorf("failed to unmarshal payload: %w", err)
		}

		// Parse filled quantity
		filledQty := 0.0
		if payload.FilledQuantity != "" {
			var err error
			filledQty, err = strconv.ParseFloat(payload.FilledQuantity, 64)
			if err != nil {
				return fmt.Errorf("failed to parse filled_quantity: %w", err)
			}
		}

		// Update order status
		if err := orderRepo.UpdateStatusByOrderID(ctx, payload.OrderID, payload.Status, filledQty); err != nil {
			return fmt.Errorf("failed to update order status: %w", err)
		}

		return nil
	}
}
