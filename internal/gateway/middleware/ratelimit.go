package middleware

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/response"
	"github.com/redis/go-redis/v9"
)

// The hash stores fractional tokens and the time of the last refill, in microseconds.
// Redis TIME keeps buckets shared across gateways independent of their clocks.
var tokenBucket = redis.NewScript(`
local capacity = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local tm = redis.call('TIME')
local now = tonumber(tm[1]) * 1000000 + tonumber(tm[2])
local state = redis.call('HMGET', KEYS[1], 'tokens', 'refilled_at')
local tokens = tonumber(state[1]) or capacity
local last = tonumber(state[2]) or now
local refill_at = math.max(last, now)
local rate = capacity / (window_ms * 1000)
tokens = math.min(capacity, tokens + (refill_at - last) * rate)
local allowed, retry_ms = 0, 0
if tokens >= 1 then
    allowed = 1
    tokens = tokens - 1
else
    retry_ms = math.ceil(((1 - tokens) / rate + refill_at - now) / 1000)
end
redis.call('HSET', KEYS[1], 'tokens', string.format('%.17g', tokens), 'refilled_at', string.format('%.0f', refill_at))
redis.call('PEXPIRE', KEYS[1], window_ms + 1000)
return {allowed, retry_ms}
`)

const limiterTimeout = 200 * time.Millisecond

var errNoRateLimitRedis = errors.New("rate limit Redis is unavailable")

type Limiter struct {
	rdb redis.UniversalClient
	m   *metrics.Gateway
}

func NewLimiter(rdb redis.UniversalClient, m *metrics.Gateway) *Limiter {
	return &Limiter{rdb: rdb, m: m}
}

// Middleware applies a configured bucket to each identity in the policy's scope.
func (l *Limiter) Middleware(p config.RateLimitPolicy) gin.HandlerFunc {
	prefix := "rl:" + p.Name + ":"
	windowMS := p.WindowSec * 1000
	args := []any{p.MaxCount, windowMS}
	return func(c *gin.Context) {
		var identity string
		if p.Scope == "user" {
			identity = strconv.FormatInt(GetUserID(c), 10)
		} else {
			identity = c.ClientIP()
		}
		allowed, retryMS, err := l.check(c.Request.Context(), prefix+identity, args)
		if err != nil {
			l.m.RateLimiterError(p.Name)
			logger.WithContext(c.Request.Context()).Warn("rate limit check failed",
				logger.S("policy", p.Name), logger.Err(err))
			c.Next()
			return
		}
		if !allowed {
			l.m.RateLimited(p.Name)
			c.Header("Retry-After", strconv.FormatInt((retryMS+999)/1000, 10))
			c.Abort()
			response.TooManyRequests(c, "rate limit exceeded")
			return
		}
		c.Next()
	}
}

func (l *Limiter) check(ctx context.Context, key string, args []any) (bool, int64, error) {
	if l.rdb == nil {
		return false, 0, errNoRateLimitRedis
	}
	ctx, cancel := context.WithTimeout(ctx, limiterTimeout)
	defer cancel()
	result, err := tokenBucket.Run(ctx, l.rdb, []string{key}, args...).Slice()
	if err != nil {
		return false, 0, err
	}
	if len(result) == 2 {
		allowed, allowedOK := result[0].(int64)
		retryMS, retryOK := result[1].(int64)
		if allowedOK && retryOK {
			return allowed != 0, retryMS, nil
		}
	}
	return false, 0, errors.New("invalid rate limiter script reply")
}
