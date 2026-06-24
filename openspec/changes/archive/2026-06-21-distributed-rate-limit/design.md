## Context

The current `RateLimit` middleware in `internal/gateway/middleware/middleware.go` uses an in-memory `map[string]*clientInfo` to track request counts per IP. This approach has several limitations:

1. **No Horizontal Scaling**: Each gateway instance maintains its own counter. Multiple instances behind a load balancer don't share state, allowing users to bypass limits by refreshing instances.
2. **Single Dimension**: Only IP-based limiting is supported. Modern applications need per-user, per-API, and combined strategies.
3. **No Persistence**: Counters reset on restart, and in-memory maps grow unbounded without cleanup.
4. **Limited Observability**: No Prometheus metrics for rate limiting events.

The Redis sliding window approach solves these by using Redis as a centralized, atomic counter with TTL-based expiry.

## Goals / Non-Goals

**Goals:**
- Implement Redis-backed sliding window rate limiting with atomic Lua script execution
- Support three dimensions: IP address, User ID, and API endpoint
- Enable policy-based configuration via YAML (e.g., `100/m`, `10/s`)
- Return HTTP 429 with `Retry-After` header when limits are exceeded
- Expose `rate_limit_blocked_total` counter for Prometheus monitoring
- Key format: `rate:{scope}:{identity}:{window}` where scope = `ip|user|api`

**Non-Goals:**
- Token bucket algorithm (consider for future enhancement with redis_cell)
- Distributed locking or consensus mechanisms
- Rate limit quotas per user tiers (future feature)
- Adaptive rate limiting based on server load

## Decisions

### Decision 1: Sliding Window vs Fixed Window vs Token Bucket

**Chosen**: Sliding Window Counter using Lua script

**Rationale**:
- Sliding window provides more accurate rate limiting than fixed window (no burst at window boundaries)
- Lua script ensures atomic read-modify-write operations
- More predictable memory usage than token bucket with distributed setup
- `redis_cell` library was considered but adds dependency; Lua script is self-contained

**Alternatives Considered**:
- Fixed Window: Simple but allows 2x requests at boundaries
- Token Bucket: Requires tracking refill rates; redis_cell adds dependency
- Sliding Window Log: Most accurate but high memory usage

### Decision 2: Key Structure

**Chosen**: `rate:{scope}:{identity}:{window}`

**Example**:
- `rate:ip:192.168.1.1:minute` - IP-level limit per minute
- `rate:user:user123:second` - User-level limit per second
- `rate:api:/api/v1/orders:minute` - API endpoint limit per minute

**Rationale**:
- Colon-separated for easy parsing and Redis key pattern matching
- Window timestamp in key handles rolling windows naturally
- Scope prefix allows targeted flushing if needed

### Decision 3: Configuration Format

**Chosen**: YAML with format `amount/period` (e.g., `100/m`, `10/s`)

```yaml
rate_limit:
  policies:
    - name: global_ip
      scope: ip
      limit: 100/m
    - name: create_order_per_user
      scope: user
      limit: 10/s
      paths: ["/api/v1/orders"]
    - name: login_per_ip
      scope: ip
      limit: 5/m
      paths: ["/api/v1/auth/login"]
```

**Rationale**:
- Human-readable format familiar from nginx, AWS API Gateway
- Easy to parse: split by `/` for amount and period
- Supports both global and path-specific policies

### Decision 4: Redis Data Structure

**Chosen**: Sorted Set (ZSET) with score = timestamp, member = unique request ID

**Operations in Lua**:
1. Remove expired entries (score < window_start)
2. Count remaining entries
3. If under limit, add new entry with current timestamp
4. Return count for decision

**Rationale**:
- ZSET allows efficient range queries by score
- Automatic TTL via periodic cleanup in Lua
- No external expiration needed (self-cleaning)

## Risks / Trade-offs

| Risk | Impact | Mitigation |
|------|--------|------------|
| Redis becomes single point of failure | All requests blocked if Redis down | Circuit breaker with fallback to allow-all; log warning |
| Lua script complexity | Harder to debug than simple INCR | Comprehensive comments; unit tests |
| Memory growth with ZSET | Many unique keys over time | Set TTL on keys (e.g., window duration + buffer) |
| Clock skew between instances | Slight inaccuracy in sliding window | Use Redis server time via TIME command, not local clock |
| Policy evaluation order | Conflicting policies | First-match-wins; order by specificity in config |

## Migration Plan

### Phase 1: Add Configuration (No Behavior Change)
1. Add `RateLimitConfig` to `pkg/config/config.go`
2. Parse YAML policies without activating them
3. No changes to middleware behavior

### Phase 2: Implement Redis Rate Limiter
1. Create `internal/gateway/middleware/ratelimit_redis.go`
2. Implement `RateLimiter` interface
3. Write Lua script for atomic operations
4. Add Prometheus counter `rate_limit_blocked_total`

### Phase 3: Activate Redis Rate Limiter
1. Replace in-memory `RateLimit()` in `middleware.go`
2. Update `cmd/gateway/main.go` to wire Redis client
3. Feature flag for gradual rollout

### Rollback
- Disable Redis rate limiter via config flag
- Fallback to in-memory implementation (keep original code as fallback)
- Revert middleware.go to use old `RateLimit` function

## Open Questions

1. **Should we support path wildcards?** (e.g., `/api/v1/orders/*`)
   - Decision: Start with exact match, add wildcard support if needed

2. **How to handle missing User ID?**
   - Decision: Fall back to IP-based limiting when user context unavailable

3. **Should limits be per-instance or global?**
   - Decision: Global (Redis-backed) by default; option for per-instance in config
