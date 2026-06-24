## 1. Configuration Setup

- [x] 1.1 Add RateLimitConfig struct to pkg/config/config.go
- [x] 1.2 Add RateLimitPolicy struct with scope, limit, paths fields
- [x] 1.3 Parse rate_limit.policies from YAML configuration
- [x] 1.4 Support limit format parsing (e.g., "100/m", "10/s")

## 2. Redis Rate Limiter Implementation

- [x] 2.1 Create internal/gateway/middleware/ratelimit_redis.go
- [x] 2.2 Implement RateLimiter interface with Check() method
- [x] 2.3 Write Lua script for sliding window algorithm using ZSET
- [x] 2.4 Implement key generation: rate:{scope}:{identity}:{window}
- [x] 2.5 Add TTL to Redis keys (window duration + buffer)

## 3. Prometheus Metrics Integration

- [x] 3.1 Add rate_limit_blocked_total counter with labels (scope, identity, policy)
- [x] 3.2 Add rate_limit_requests_total counter for all requests
- [x] 3.3 Add rate_limit_errors_total counter for Redis errors

## 4. Middleware Integration

- [x] 4.1 Modify RateLimit() in middleware.go to use Redis rate limiter
- [x] 4.2 Extract identity from request (IP, User ID from context)
- [x] 4.3 Return HTTP 429 with Retry-After header when blocked
- [x] 4.4 Implement graceful fallback when Redis unavailable

## 5. Gateway Integration

- [x] 5.1 Wire Redis client in cmd/gateway/main.go
- [x] 5.2 Initialize rate limiter with config
- [x] 5.3 Register middleware in router setup
