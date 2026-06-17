// Package main User Service 主入口
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/user/repository"
	"github.com/linxun2025/exchange-project/internal/user/server"
	"github.com/linxun2025/exchange-project/internal/user/service"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/tracing"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// gRPC server performance options
const (
	grpcMaxConcurrentStreams = 100
	grpcMaxRecvMsgSize      = 1024 * 1024 * 4  // 4MB
	grpcMaxSendMsgSize      = 1024 * 1024 * 4  // 4MB
	grpcReadBufferSize      = 32 * 1024        // 32KB
	grpcWriteBufferSize     = 32 * 1024        // 32KB
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

	// 初始化 Redis 客户端
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})

	// 初始化数据库连接
	db, err := gorm.Open(mysql.Open(cfg.Database.DSN()), &gorm.Config{})
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}

	// 初始化仓储层
	userRepo := repository.NewUserRepository(db, redisClient)

	// 初始化服务层
	userSvc := service.NewUserService(userRepo, redisClient, cfg.JWT.Secret, time.Duration(cfg.JWT.ExpireTime)*time.Hour)

	// 创建 gRPC 服务器
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		logger.Fatal("failed to listen", zap.Error(err))
	}

	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(grpcx.UnaryServerRequestID()),
		grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams),
		grpc.MaxRecvMsgSize(grpcMaxRecvMsgSize),
		grpc.MaxSendMsgSize(grpcMaxSendMsgSize),
		grpc.ReadBufferSize(grpcReadBufferSize),
		grpc.WriteBufferSize(grpcWriteBufferSize),
	)

	// 注册 gRPC 服务
	userpb.RegisterUserServiceServer(grpcServer, server.NewUserServer(userSvc))

	// 启用 gRPC 反射（用于调试）
	reflection.Register(grpcServer)

	// 启动服务
	go func() {
		logger.Info("gRPC server starting", logger.S("address", ":50051"))
		if err := grpcServer.Serve(lis); err != nil {
			logger.Fatal("gRPC server failed to serve", zap.Error(err))
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down gRPC server...")

	grpcServer.GracefulStop()

	logger.Info("gRPC server stopped")
}
