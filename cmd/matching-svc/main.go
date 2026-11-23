// Package main starts the matching service.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/matching/server"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const grpcAddr = ":50053"
const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		logger.Fatal("matching service stopped with error", logger.Err(err))
	}
	logger.Sync()
}

func run(ctx context.Context) (retErr error) {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := logger.Init(cfg.App.Environment); err != nil {
		return err
	}
	shutdownTracing, err := tracing.Init(ctx, "matching-svc", "")
	if err != nil {
		logger.Warn("failed to init tracing", logger.Err(err))
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracing(ctx); err != nil {
				logger.Warn("failed to shut down tracing", logger.Err(err))
			}
		}()
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	engineCfg, err := matchingConfig(cfg.Symbols, metrics.NewMatching(reg))
	if err != nil {
		return err
	}
	matcher, err := engine.New(engineCfg)
	if err != nil {
		return fmt.Errorf("matching recovery failed: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		retErr = errors.Join(retErr, matcher.Close(ctx))
	}()
	if ctx.Err() != nil {
		return nil
	}

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return err
	}
	defer lis.Close()
	srv, health := grpcx.NewServer(reg)
	matchingpb.RegisterMatchingServiceServer(srv, server.NewMatchingServer(matcher, cfg.Symbols))
	health.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	metricsCtx, stopMetrics := context.WithCancel(ctx)
	defer stopMetrics()
	serveErr := make(chan error, 2)
	go func() { serveErr <- srv.Serve(lis) }()
	go func() { serveErr <- metrics.Serve(metricsCtx, cfg.Metrics.Addr, reg) }()
	logger.Info("matching service started", logger.S("address", grpcAddr))

	select {
	case <-ctx.Done():
	case retErr = <-serveErr:
	}
	health.Shutdown()
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case <-stopped:
	case <-timer.C:
		srv.Stop()
	}
	return retErr
}

func matchingConfig(symbols []string, m *metrics.Matching) (engine.Config, error) {
	cfg := engine.Config{
		WALDir:      envOr("WAL_DIR", "data/wal"),
		SnapshotDir: envOr("SNAPSHOT_DIR", "data/snapshot"),
		Symbols:     symbols,
		Metrics:     m,
	}
	switch value := envOr("WAL_SYNC", "fsync"); value {
	case "fsync":
		cfg.Sync = wal.SyncFsync
	case "none":
		cfg.Sync = wal.SyncNone
	default:
		return cfg, fmt.Errorf("invalid WAL_SYNC %q: want fsync or none", value)
	}
	value := envOr("SNAPSHOT_EVERY", "50000")
	every, err := strconv.ParseUint(value, 10, 64)
	if err != nil || every == 0 {
		return cfg, fmt.Errorf("invalid SNAPSHOT_EVERY %q: want a positive record count", value)
	}
	cfg.SnapshotEvery = every
	value = envOr("SNAPSHOT_INTERVAL", "60s")
	interval, err := time.ParseDuration(value)
	if err != nil || interval <= 0 {
		return cfg, fmt.Errorf("invalid SNAPSHOT_INTERVAL %q: want a positive duration", value)
	}
	cfg.SnapshotInterval = interval
	return cfg, nil
}

func envOr(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
