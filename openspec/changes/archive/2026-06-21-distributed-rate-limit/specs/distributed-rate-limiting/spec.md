# Distributed Rate Limiting

## ADDED Requirements

### Requirement: Redis Sliding Window Rate Limiter

The system SHALL implement a Redis-backed sliding window rate limiter using Lua scripts for atomic operations. The rate limiter SHALL support multiple policy configurations with different scopes (IP, User, API) and limits.

#### Scenario: IP-based rate limiting with Redis
- **WHEN** a request arrives from IP `192.168.1.1` with policy `global_ip: 100/m`
- **THEN** the system SHALL increment the Redis counter and allow the request if under limit
- **AND** the Redis key SHALL be `rate:ip:192.168.1.1:{window_timestamp}`

#### Scenario: User-based rate limiting
- **WHEN** a request arrives from user `user123` with policy `create_order_per_user: 10/s`
- **THEN** the system SHALL check the user identity from request context
- **AND** SHALL use Redis key `rate:user:user123:{window_timestamp}`

#### Scenario: API endpoint rate limiting
- **WHEN** a request arrives for endpoint `/api/v1/orders` with policy `orders_api: 100/m`
- **THEN** the system SHALL use the API path as the identity
- **AND** SHALL use Redis key `rate:api:/api/v1/orders:{window_timestamp}`

#### Scenario: Rate limit exceeded returns 429
- **WHEN** a request causes the rate limit counter to exceed the configured limit
- **THEN** the system SHALL return HTTP status code 429 (Too Many Requests)
- **AND** SHALL include `Retry-After` header with seconds until reset

#### Scenario: Prometheus metrics exposed
- **WHEN** a request is blocked by rate limiting
- **THEN** the system SHALL increment counter `rate_limit_blocked_total{scope, identity, policy}`
- **AND** the metric SHALL be scrapable by Prometheus

### Requirement: Configuration-driven Policy Management

The system SHALL load rate limit policies from YAML configuration and support multiple policies with different scopes, limits, and path filters.

#### Scenario: Policy configuration parsing
- **WHEN** config contains `rate_limit.policies[].limit: "100/m"`
- **THEN** the system SHALL parse `100` as the request count
- **AND** SHALL parse `m` as minutes (valid: `s`, `m`, `h`)

#### Scenario: Path-specific policy application
- **WHEN** a policy specifies `paths: ["/api/v1/orders"]`
- **THEN** the policy SHALL only apply to requests matching those exact paths
- **AND** other paths SHALL not be affected by this policy

#### Scenario: Multiple policies evaluated
- **WHEN** multiple policies match a request (e.g., global IP + user limits)
- **THEN** the system SHALL check ALL matching policies
- **AND** SHALL block if ANY policy limit is exceeded

### Requirement: Graceful Degradation

The system SHALL handle Redis connection failures gracefully by falling back to allowing requests while logging errors.

#### Scenario: Redis connection failure fallback
- **WHEN** Redis is unavailable or connection times out
- **THEN** the system SHALL log an error with `level: error`
- **AND** SHALL allow the request to pass (fail-open)
- **AND** SHALL NOT increment rate limit metrics

#### Scenario: Lua script execution failure
- **WHEN** Lua script returns an error
- **THEN** the system SHALL log the error details
- **AND** SHALL allow the request to pass
- **AND** SHALL increment `rate_limit_errors_total` counter

### Requirement: Sliding Window Algorithm

The system SHALL implement sliding window rate limiting using Redis Sorted Sets (ZSET), removing expired entries within the Lua script to ensure atomicity.

#### Scenario: Window slides with time
- **WHEN** a request arrives at `T=60s` with window `60s`
- **THEN** entries older than `T-60s` SHALL be removed
- **AND** only entries within the current window count toward the limit

#### Scenario: Atomic operation via Lua script
- **WHEN** the rate limiter processes a request
- **THEN** all Redis operations (remove expired, count, add entry) SHALL execute atomically
- **AND** no race conditions SHALL occur between concurrent requests

#### Scenario: Key expiration
- **WHEN** a sliding window key is created
- **THEN** the key SHALL have TTL set to window duration plus buffer (e.g., 2x window)
- **AND** stale keys SHALL be automatically cleaned up by Redis
