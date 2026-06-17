// Package main API Gateway 主入口
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/router"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/tracing"
	"go.uber.org/zap"
)

func main() {
	// 加载配置
	cfg, err := config.Load("")
	if err != nil {
		logger.Fatal("failed to load config", logger.Err(err))
	}

	// 初始化日志
	if err := logger.Init(cfg.App.Environment); err != nil {
		logger.Fatal("failed to init logger", logger.Err(err))
	}
	defer logger.Sync()

	logger.Info("starting API Gateway",
		logger.S("name", cfg.App.Name),
		logger.S("environment", cfg.App.Environment),
	)

	// 初始化 OpenTelemetry tracing
	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	tracingShutdown, err := tracing.Init(context.Background(), "gateway", otelEndpoint)
	if err != nil {
		logger.Warn("failed to init tracing", logger.Err(err))
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tracingShutdown(ctx); err != nil {
				logger.Error("failed to shutdown tracing", logger.Err(err))
			}
		}()
	}

	// 初始化 gRPC 客户端
	clients, err := client.NewClients(&client.Config{
		UserGRPCAddr:     cfg.GRPC.UserGRPCAddr,
		OrderGRPCAddr:   cfg.GRPC.OrderGRPCAddr,
		MatchingGRPCAddr: cfg.GRPC.MatchingGRPCAddr,
	})
	if err != nil {
		logger.Fatal("failed to create gRPC clients", zap.Error(err))
	}
	defer clients.Close()

	// 创建 Gin 引擎
	r := router.NewRouter(cfg, clients)

	// 创建 HTTP 服务器
	srv := &http.Server{
		Addr:         cfg.Server.Address(),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 启动服务器
	go func() {
		logger.Info("server starting", logger.S("address", cfg.Server.Address()))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server failed to start", zap.Error(err))
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")

	// 优雅关闭
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server forced to shutdown", zap.Error(err))
	}

	logger.Info("server stopped")
}
