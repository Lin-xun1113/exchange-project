## Why

The current rate limiting implementation uses in-memory tracking per instance, which doesn't scale horizontally in distributed deployments. When multiple gateway instances run behind a load balancer, each maintains its own counter, allowing requests to bypass limits. Additionally, the existing implementation only supports IP-based limiting and lacks the flexibility for per-user, per-API, or combined rate limit strategies needed for production traffic management.

## What Changes

- Replace in-memory `RateLimit` middleware with Redis-based sliding window counter using Lua scripts for atomic operations
- Add multi-dimensional rate limiting support: IP, User ID, and API endpoint combinations
- Implement configurable rate limit policies via YAML configuration
- Add `Retry-After` header in 429 responses
- Expose `rate_limit_blocked_total` Prometheus counter metric
- Key format: `rate:{scope}:{identity}:{window}` for flexible Redis key management

## Capabilities

### New Capabilities

- `distributed-rate-limiting`: Redis-backed sliding window rate limiter with support for IP, user, and API dimension combinations. Supports configurable policies, atomic Lua script operations, and Prometheus metrics integration.

### Modified Capabilities

- `gateway-middleware`: Modify rate limiting behavior from in-memory to Redis-based distributed implementation (delta spec)

## Impact

- **Modified Files**:
  - `internal/gateway/middleware/middleware.go` - Replace `RateLimit` to use Redis implementation
  - `pkg/config/config.go` - Add `RateLimitConfig` struct and YAML config support
- **New Files**:
  - `internal/gateway/middleware/ratelimit_redis.go` - Redis sliding window implementation
- **Dependencies**:
  - Redis connection (already configured)
  - `github.com/redis/go-redis/v9` for Redis operations
- **Metrics**:
  - New counter: `rate_limit_blocked_total{scope, identity, policy}` for monitoring blocked requests
