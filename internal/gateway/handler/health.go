package handler

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/pkg/response"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const healthTimeout = 200 * time.Millisecond

type grpcDependency struct {
	name   string
	health healthpb.HealthClient
}

type Health struct {
	services [3]grpcDependency
	rdb      redis.UniversalClient
}

func NewHealth(conns map[string]*grpc.ClientConn, rdb redis.UniversalClient) *Health {
	h := &Health{rdb: rdb}
	for i, target := range [...]string{client.UserTarget, client.OrderTarget, client.MatchingTarget} {
		h.services[i].name = target
		if conn := conns[target]; conn != nil {
			h.services[i].health = healthpb.NewHealthClient(conn)
		}
	}
	return h
}

func (h *Health) Live(c *gin.Context) {
	response.Success(c, gin.H{"status": "ok", "timestamp": time.Now().Unix()})
}

func (h *Health) Ready(c *gin.Context) {
	parent := c.Request.Context()
	var wg sync.WaitGroup
	var serving [3]bool
	for i, svc := range h.services {
		if svc.health == nil {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(parent, healthTimeout)
			defer cancel()
			status, err := svc.health.Check(ctx, &healthpb.HealthCheckRequest{})
			serving[i] = err == nil && status.GetStatus() == healthpb.HealthCheckResponse_SERVING
		})
	}
	var redisOK bool
	if h.rdb != nil {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(parent, healthTimeout)
			defer cancel()
			redisOK = h.rdb.Ping(ctx).Err() == nil
		})
	}
	wg.Wait()

	var failed []string
	for i, ok := range serving {
		if !ok {
			failed = append(failed, h.services[i].name)
		}
	}
	if !redisOK {
		failed = append(failed, "redis")
	}
	if len(failed) == 0 {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
		return
	}
	slices.Sort(failed)
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "failed": failed})
}
