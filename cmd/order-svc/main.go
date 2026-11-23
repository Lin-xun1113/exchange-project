// Package main runs the order service.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/order/server"
	"github.com/linxun2025/exchange-project/internal/order/service"
	"github.com/linxun2025/exchange-project/internal/order/workflow"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		logger.Fatal("order service failed", logger.Err(err))
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := logger.Init(cfg.App.Environment); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer logger.Sync()
	concurrency, err := workerConcurrency(os.Getenv("WORKER_CONCURRENCY"))
	if err != nil {
		return err
	}
	shutdownTracing, err := tracing.Init(ctx, "order-svc", "")
	if err != nil {
		logger.Warn("failed to init tracing", logger.Err(err))
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracing(shutdownCtx); err != nil {
				logger.Warn("failed to shut down tracing", logger.Err(err))
			}
		}()
	}

	db, err := gorm.Open(mysql.Open(cfg.Database.DSN()), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	pool, err := db.DB()
	if err != nil {
		return fmt.Errorf("configure database pool: %w", err)
	}
	defer pool.Close()
	pool.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	pool.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	pool.SetConnMaxLifetime(time.Duration(cfg.Database.ConnMaxLife) * time.Minute)

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := metrics.NewOrder(reg)
	clientMetrics := metrics.GRPCClientInterceptor(reg)
	userConn, err := grpcx.Dial(cfg.GRPC.UserGRPCAddr, clientMetrics)
	if err != nil {
		return fmt.Errorf("dial user-svc: %w", err)
	}
	defer userConn.Close()
	matchingConn, err := grpcx.Dial(cfg.GRPC.MatchingGRPCAddr, clientMetrics)
	if err != nil {
		return fmt.Errorf("dial matching-svc: %w", err)
	}
	defer matchingConn.Close()

	const addr = ":50052"
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	defer listener.Close()
	grpcServer, health := grpcx.NewServer(reg)
	orderpb.RegisterOrderServiceServer(grpcServer, server.NewOrderServer(service.New(db, cfg.Symbols, m)))
	worker := workflow.NewWorker(db,
		workflow.Handlers(db, userpb.NewUserServiceClient(userConn), matchingpb.NewMatchingServiceClient(matchingConn)),
		workflow.Options{Concurrency: concurrency}, m)

	workCtx, stopWork := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWork()
	metricsDone := make(chan error, 1)
	go func() { metricsDone <- metrics.Serve(workCtx, cfg.Metrics.Addr, reg) }()
	grpcDone := make(chan error, 1)
	health.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	go func() { grpcDone <- grpcServer.Serve(listener) }()
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workCtx) }()
	logger.Info("order gRPC server started", logger.S("address", addr), logger.I("worker_concurrency", concurrency))

	select {
	case <-ctx.Done():
	case err = <-grpcDone:
		if err != nil {
			err = fmt.Errorf("serve order RPCs: %w", err)
		}
	case err = <-metricsDone:
		if err != nil {
			err = fmt.Errorf("serve order metrics: %w", err)
		}
	}
	health.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	gracefulStop(grpcServer)
	stopWork()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case workerErr := <-workerDone:
		if err == nil {
			err = workerErr
		}
	case <-timer.C:
		if err == nil {
			err = fmt.Errorf("timed out draining order worker")
		}
	}
	return err
}

func gracefulStop(srv *grpc.Server) {
	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		srv.Stop()
	}
}

func workerConcurrency(raw string) (int, error) {
	if raw == "" {
		return 4, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("WORKER_CONCURRENCY must be a positive integer")
	}
	return n, nil
}
