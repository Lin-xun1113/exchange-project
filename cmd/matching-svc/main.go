// Package main Matching Service 主入口
package main

import (
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/linxun2025/exchange-project/api/gen/matching/v1"
	"github.com/linxun2025/exchange-project/internal/matching/client"
	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/matching/server"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// gRPC server performance options
const (
	grpcMaxConcurrentStreams = 100
	grpcMaxRecvMsgSize      = 1024 * 1024 * 4 // 4MB
	grpcMaxSendMsgSize      = 1024 * 1024 * 4 // 4MB
	grpcReadBufferSize      = 32 * 1024       // 32KB
	grpcWriteBufferSize     = 32 * 1024       // 32KB
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

	logger.Info("starting Matching Service",
		logger.S("name", cfg.App.Name),
		logger.S("environment", cfg.App.Environment),
	)

	// 初始化数据库连接
	db, err := gorm.Open(mysql.Open(cfg.Database.DSN()), &gorm.Config{})
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}

	// 初始化交易仓储
	tradeRepo := repository.NewTradeRepository(db)

	// 创建撮合引擎
	matcher := engine.NewMatcher(engine.Config{
		Workers:   10,
		QueueSize: 1000,
	})
	defer matcher.Shutdown()

	// 创建 Order Service gRPC 客户端
	orderClient, err := client.NewOrderClient(cfg.GRPC.OrderGRPCAddr)
	if err != nil {
		logger.Fatal("failed to create order client", zap.Error(err))
	}
	defer orderClient.Close()

	// 创建 gRPC 服务器
	lis, err := net.Listen("tcp", ":50053")
	if err != nil {
		logger.Fatal("failed to listen", zap.Error(err))
	}

	grpcServer := grpc.NewServer(
		grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams),
		grpc.MaxRecvMsgSize(grpcMaxRecvMsgSize),
		grpc.MaxSendMsgSize(grpcMaxSendMsgSize),
		grpc.ReadBufferSize(grpcReadBufferSize),
		grpc.WriteBufferSize(grpcWriteBufferSize),
	)

	// 注册 gRPC 服务
	matchingpb.RegisterMatchingServiceServer(grpcServer, server.NewMatchingServer(matcher, orderClient, tradeRepo))

	// 启用 gRPC 反射
	reflection.Register(grpcServer)

	// 启动服务
	go func() {
		logger.Info("gRPC server starting", logger.S("address", ":50053"))
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
	matcher.Shutdown()

	logger.Info("Matching Service stopped")
}
