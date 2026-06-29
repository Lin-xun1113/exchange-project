// Package main Matching Service 主入口
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/linxun2025/exchange-project/api/gen/matching/v1"
	"github.com/linxun2025/exchange-project/internal/matching/client"
	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/matching/server"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/internal/order/repository"
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

const (
	grpcMaxConcurrentStreams = 100
	grpcMaxRecvMsgSize      = 1024 * 1024 * 4
	grpcMaxSendMsgSize      = 1024 * 1024 * 4
	grpcReadBufferSize      = 32 * 1024
	grpcWriteBufferSize     = 32 * 1024
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		logger.Fatal("failed to load config", logger.Err(err))
	}

	if err := logger.Init(cfg.App.Environment); err != nil {
		logger.Fatal("failed to init logger", logger.Err(err))
	}
	defer logger.Sync()

	logger.Info("starting Matching Service",
		logger.S("name", cfg.App.Name),
		logger.S("environment", cfg.App.Environment),
	)

	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	tracingShutdown, err := tracing.Init(context.Background(), "matching-svc", otelEndpoint)
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

	db, err := gorm.Open(mysql.Open(cfg.Database.DSN()), &gorm.Config{})
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}

	tradeRepo := repository.NewTradeRepository(db)

	// WAL and Snapshot directories from config or environment.
	walDir := getEnvOrDefault("WAL_DIR", "data/wal")
	snapshotDir := getEnvOrDefault("SNAPSHOT_DIR", "data/snapshots")
	maxTradesPerSnapshot := getEnvIntOrDefault("MAX_TRADES_PER_SNAPSHOT", 1000)
	snapshotInterval := time.Duration(getEnvIntOrDefault("SNAPSHOT_INTERVAL_SECONDS", 60)) * time.Second

	// WAL sync configuration
	walSyncMode := parseWALSyncMode(getEnvOrDefault("WAL_SYNC_MODE", "byduration"))
	walSyncInterval := time.Duration(getEnvIntOrDefault("WAL_SYNC_INTERVAL_MS", 1)) * time.Millisecond
	walSyncEvery := uint64(getEnvIntOrDefault("WAL_SYNC_EVERY", 0))

	logger.Info("WAL and snapshot config",
		logger.S("wal_dir", walDir),
		logger.S("snapshot_dir", snapshotDir),
		logger.I("max_trades_per_snapshot", maxTradesPerSnapshot),
		logger.S("snapshot_interval", snapshotInterval.String()),
		logger.S("wal_sync_mode", walSyncMode.String()),
		logger.S("wal_sync_interval", walSyncInterval.String()),
		logger.I64("wal_sync_every", int64(walSyncEvery)),
	)

	// Create WAL manager.
	walManager := engine.NewWALManager(walDir, walSyncMode, walSyncEvery, walSyncInterval)
	defer walManager.Close()

	// Create matcher.
	matcher := engine.NewMatcher(engine.Config{
		ActorTimeout: 5 * time.Second,
	})
	defer matcher.Shutdown()

	// Set WAL manager on matcher.
	matcher.SetWALManager(walManager)

	// Run crash recovery before accepting requests.
	if err := matcher.Recover(engine.RecoveryConfig{
		WALDir:              walDir,
		SnapshotDir:         snapshotDir,
		MaxTradesPerSnapshot: maxTradesPerSnapshot,
		SnapshotInterval:    snapshotInterval,
	}); err != nil {
		logger.Error("crash recovery failed", logger.Err(err))
	}

	orderClient, err := client.NewOrderClient(cfg.GRPC.OrderGRPCAddr)
	if err != nil {
		logger.Fatal("failed to create order client", zap.Error(err))
	}
	defer orderClient.Close()

	lis, err := net.Listen("tcp", ":50053")
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

	matchingpb.RegisterMatchingServiceServer(grpcServer, server.NewMatchingServer(matcher, orderClient, tradeRepo))
	reflection.Register(grpcServer)

	go func() {
		logger.Info("gRPC server starting", logger.S("address", ":50053"))
		if err := grpcServer.Serve(lis); err != nil {
			logger.Fatal("gRPC server failed to serve", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down Matching Service...")

	grpcServer.GracefulStop()
	matcher.Shutdown()

	logger.Info("Matching Service stopped")
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvIntOrDefault(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func parseWALSyncMode(mode string) wal.SyncMode {
	switch mode {
	case "none":
		return wal.SyncNone
	case "always":
		return wal.SyncAlways
	case "bycount":
		return wal.SyncByCount
	case "byduration":
		return wal.SyncByDuration
	default:
		return wal.SyncByDuration // Default to Group Commit
	}
}
