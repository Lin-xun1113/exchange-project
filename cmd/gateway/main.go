// Package main starts the HTTP gateway.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/router"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		logger.Fatal("gateway stopped with error", logger.Err(err))
	}
	logger.Sync()
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := logger.Init(cfg.App.Environment); err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}

	shutdownTracing, err := tracing.Init(ctx, "gateway", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		logger.Warn("failed to initialize tracing", logger.Err(err))
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracing(shutdownCtx); err != nil {
				logger.Warn("failed to shut down tracing", logger.Err(err))
			}
		}()
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:                  cfg.Redis.Addr(),
		Password:              cfg.Redis.Password,
		DB:                    cfg.Redis.DB,
		PoolSize:              cfg.Redis.PoolSize,
		DialerRetries:         1,
		MaxRetries:            -1,
		ContextTimeoutEnabled: true,
	})
	defer rdb.Close()
	pingCtx, cancelPing := context.WithTimeout(ctx, time.Second)
	err = rdb.Ping(pingCtx).Err()
	cancelPing()
	if err != nil {
		logger.Warn("Redis is unavailable at startup", logger.Err(err))
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := metrics.NewGateway(reg)
	clients, err := client.New(cfg, m, reg)
	if err != nil {
		return fmt.Errorf("initialize gRPC clients: %w", err)
	}
	defer func() {
		if err := clients.Close(); err != nil {
			logger.Warn("failed to close gRPC clients", logger.Err(err))
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	r, err := router.New(cfg, clients, rdb, m)
	if err != nil {
		return fmt.Errorf("initialize router: %w", err)
	}
	srv := &http.Server{
		Addr:              cfg.Server.Address(),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	serverErr := make(chan error, 2)
	go func() {
		if err := metrics.Serve(ctx, cfg.Metrics.Addr, reg); err != nil {
			serverErr <- fmt.Errorf("serve metrics: %w", err)
		}
	}()
	go func() {
		logger.Info("starting gateway HTTP server", logger.S("address", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("serve HTTP: %w", err)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-serverErr:
	}
	cancel()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return errors.Join(runErr, fmt.Errorf("shut down HTTP server: %w", err), srv.Close())
	}
	return runErr
}
