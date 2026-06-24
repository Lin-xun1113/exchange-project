package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
)

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	Name    string
	MaxReq  uint32
	Timeout time.Duration
}

var cbConfig = CircuitBreakerConfig{
	Name:    "matching-service",
	MaxReq:  100,
	Timeout: 30 * time.Second,
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker() *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name: cbConfig.Name,
		MaxRequests: cbConfig.MaxReq,
		Timeout:     cbConfig.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.Requests >= 20 && failureRatio >= 0.5
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Warn("circuit breaker state changed",
				logger.S("name", name),
				logger.S("from", fmt.Sprintf("%s", from)),
				logger.S("to", fmt.Sprintf("%s", to)),
			)
			recordCircuitBreakerState(name, to)
		},
	})
}

func recordCircuitBreakerState(name string, state gobreaker.State) {
	var stateVal float64
	switch state {
	case gobreaker.StateClosed:
		stateVal = 0
	case gobreaker.StateOpen:
		stateVal = 1
	case gobreaker.StateHalfOpen:
		stateVal = 2
	}
	metrics.GetMetrics().RecordCircuitBreakerState(name, stateVal)
}

var (
	slidingWindowScript = redis.NewScript(`
		local key = KEYS[1]
		local now = tonumber(ARGV[1])
		local window = tonumber(ARGV[2])
		local max = tonumber(ARGV[3])
		local ttl = tonumber(ARGV[4])
		local request_id = ARGV[5]

		-- Calculate window start
		local window_start = now - window

		-- Remove expired entries
		redis.call('ZREMRANGEBYSCORE', key, '-inf', window_start)

		-- Count current requests in window
		local count = redis.call('ZCARD', key)

		if count < max then
			-- Add new request
			redis.call('ZADD', key, now, request_id)
			-- Set TTL
			redis.call('EXPIRE', key, ttl)
			return {1, count + 1, max - count - 1}
		else
			return {0, count, 0}
		end
	`)
)

type RateLimitResult struct {
	Allowed   bool
	Current   int64
	Remaining int64
	RetryAfter int64
}

type RateLimiter interface {
	Check(ctx context.Context, scope, identity string, policy *config.RateLimitPolicy) (*RateLimitResult, error)
}

type RedisRateLimiter struct {
	client *redis.Client
}

func NewRedisRateLimiter(client *redis.Client) *RedisRateLimiter {
	return &RedisRateLimiter{client: client}
}

func (r *RedisRateLimiter) Check(ctx context.Context, scope, identity string, policy *config.RateLimitPolicy) (*RateLimitResult, error) {
	key := r.buildKey(scope, identity, policy.WindowSec)
	now := time.Now().UnixMilli()
	windowMs := policy.WindowSec * 1000
	ttl := policy.WindowSec + 60 // TTL = window + 1 minute buffer
	requestID := fmt.Sprintf("%d-%s", now, generateRequestID())

	result, err := slidingWindowScript.Run(ctx, r.client, []string{key},
		now,
		windowMs,
		policy.MaxCount,
		ttl,
		requestID,
	).Slice()

	if err != nil {
		logger.Error("rate limit script error",
			logger.Err(err),
			logger.S("key", key),
		)
		return nil, err
	}

	allowed := result[0].(int64) == 1
	current := result[1].(int64)
	remaining := result[2].(int64)

	return &RateLimitResult{
		Allowed:    allowed,
		Current:    current,
		Remaining:  remaining,
		RetryAfter: r.calculateRetryAfter(now, windowMs, policy.WindowSec),
	}, nil
}

func (r *RedisRateLimiter) buildKey(scope, identity string, windowSec int64) string {
	windowStart := r.getWindowStart(windowSec)
	return fmt.Sprintf("rate:%s:%s:%d", scope, identity, windowStart)
}

func (r *RedisRateLimiter) getWindowStart(windowSec int64) int64 {
	now := time.Now().Unix()
	return (now / windowSec) * windowSec
}

func (r *RedisRateLimiter) calculateRetryAfter(nowMs int64, windowMs int64, windowSec int64) int64 {
	nowSec := nowMs / 1000
	currentWindowStart := (nowSec / windowSec) * windowSec
	nextWindowSec := currentWindowStart + windowSec
	return nextWindowSec - nowSec
}

func GetIdentity(c *gin.Context, scope string) string {
	switch scope {
	case "ip":
		return c.ClientIP()
	case "user":
		if userID, exists := c.Get("user_id"); exists {
			return fmt.Sprintf("%v", userID)
		}
		return c.ClientIP()
	case "api":
		return c.FullPath()
	default:
		return c.ClientIP()
	}
}

func RateLimitByPolicy(limiter RateLimiter, policies []config.RateLimitPolicy, cb *gobreaker.CircuitBreaker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(policies) == 0 {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		for _, policy := range policies {
			if !policy.MatchPath(path) {
				continue
			}

			identity := GetIdentity(c, policy.Scope)

			// Use circuit breaker to protect rate limit check
			result, err := cb.Execute(func() (interface{}, error) {
				result, checkErr := limiter.Check(c.Request.Context(), policy.Scope, identity, &policy)
				if checkErr != nil {
					return nil, checkErr
				}

				// Record rate limit request metric
				metrics.GetMetrics().RecordRateLimitRequest(policy.Scope, policy.Name)

				if !result.Allowed {
					return result, fmt.Errorf("rate limit exceeded")
				}
				return result, nil
			})

			if err != nil {
				logger.Warn("rate limit exceeded or circuit breaker open",
					logger.S("client_ip", c.ClientIP()),
					logger.S("path", path),
					logger.S("policy", policy.Name),
					logger.S("scope", policy.Scope),
					logger.S("error", err.Error()),
				)
				metrics.GetMetrics().IncRateLimitBlocked(policy.Scope, identity, policy.Name)

				// Check if circuit breaker is open
				if cb.State() == gobreaker.StateOpen {
					c.AbortWithStatusJSON(503, gin.H{
						"code":    503,
						"message": "service temporarily unavailable (circuit breaker open)",
					})
					return
				}

				// Extract RetryAfter from result if available
				retryAfter := int64(0)
				if res, ok := result.(*RateLimitResult); ok {
					retryAfter = res.RetryAfter
				}
				c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
				c.AbortWithStatusJSON(429, gin.H{
					"code":    429,
					"message": "rate limit exceeded",
				})
				return
			}

			// Set rate limit headers for successful requests
			if res, ok := result.(*RateLimitResult); ok {
				c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", res.Current+res.Remaining))
				c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", res.Remaining))
				c.Header("X-RateLimit-Scope", policy.Scope)
			}

			c.Next()
			return
		}

		c.Next()
	}
}
