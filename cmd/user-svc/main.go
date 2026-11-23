// Package main User Service 主入口
package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/user/ledger"
	"github.com/linxun2025/exchange-project/internal/user/repository"
	"github.com/linxun2025/exchange-project/internal/user/server"
	"github.com/linxun2025/exchange-project/internal/user/service"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("failed to load config", logger.Err(err))
	}

	// 初始化日志
	if err := logger.Init(cfg.App.Environment); err != nil {
		logger.Fatal("failed to init logger", logger.Err(err))
	}
	defer logger.Sync()

	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	logger.Info("starting User Service",
		logger.S("name", cfg.App.Name),
		logger.S("environment", cfg.App.Environment),
	)

	// 初始化 OpenTelemetry tracing
	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	tracingShutdown, err := tracing.Init(context.Background(), "user-svc", otelEndpoint)
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

	// 初始化数据库连接
	db, err := gorm.Open(mysql.Open(cfg.Database.DSN()), &gorm.Config{})
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}

	sqlDB, err := db.DB()
	if err != nil {
		logger.Fatal("failed to get database connection pool", zap.Error(err))
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.Database.ConnMaxLife) * time.Minute)

	// 创建 gRPC 服务器
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		logger.Fatal("failed to listen", zap.Error(err))
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	userLedger := ledger.New(db, metrics.NewUser(reg))
	userSvc := service.NewUserService(repository.NewUserRepository(db))
	grpcServer, health := grpcx.NewServer(reg)
	userpb.RegisterUserServiceServer(grpcServer, server.NewUserServer(userSvc, userLedger))
	health.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	metricsCtx, stopMetrics := context.WithCancel(context.Background())
	defer stopMetrics()
	go func() {
		if err := metrics.Serve(metricsCtx, cfg.Metrics.Addr, reg); err != nil {
			logger.Error("metrics server failed", zap.Error(err))
		}
	}()

	// 启动服务
	go func() {
		logger.Info("gRPC server starting", logger.S("address", ":50051"))
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			logger.Fatal("gRPC server failed to serve", zap.Error(err))
		}
	}()

	<-ctx.Done()
	stopSignals()

	logger.Info("shutting down gRPC server...")
	health.Shutdown()
	forceStop := time.AfterFunc(10*time.Second, grpcServer.Stop)

	grpcServer.GracefulStop()
	forceStop.Stop()

	logger.Info("gRPC server stopped")
}
