# Exchange Project Metrics Guide

This document describes all Prometheus metrics exposed by the exchange project.

## Endpoint

```
GET /metrics
```

All metrics are exposed in Prometheus text format. The endpoint is secured via the same authentication as other API endpoints.

## HTTP Metrics

### `http_requests_total`

**Type:** Counter

**Labels:** `method`, `path`, `status`

**Description:** Total number of HTTP requests processed by the gateway.

**Query Examples:**

```promql
# Requests per second by path
rate(http_requests_total[5m])

# Error rate (5xx responses)
sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))
```

### `http_request_duration_seconds`

**Type:** Histogram

**Labels:** `method`, `path`

**Description:** HTTP request duration in seconds.

**Query Examples:**

```promql
# P99 latency by path
histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[5m]))

# Average latency
rate(http_request_duration_seconds_sum[5m]) / rate(http_request_duration_seconds_count[5m])
```

## Order Metrics

### `order_created_total`

**Type:** Counter

**Labels:** `side` (`buy`, `sell`), `symbol` (e.g., `BTC-USDT`)

**Description:** Total number of orders created.

**Query Examples:**

```promql
# Orders per second by symbol
rate(order_created_total[1m])

# Order distribution by side
sum by (side) (rate(order_created_total[5m]))
```

### `order_cancelled_total`

**Type:** Counter

**Labels:** `side`, `symbol`

**Description:** Total number of orders cancelled.

**Query Examples:**

```promql
# Cancellation rate
sum(rate(order_cancelled_total[5m])) / sum(rate(order_created_total[5m]))
```

### `order_fill_rate`

**Type:** Histogram

**Labels:** `side`, `symbol`

**Description:** Distribution of order fill rates (filled quantity / original quantity). Values range from 0 to 1.

**Query Examples:**

```promql
# Average fill rate
rate(order_fill_rate_sum[5m]) / rate(order_fill_rate_count[5m])

# Full fill ratio (orders that filled 100%)
sum(rate(order_fill_rate_bucket{le="1.0"}[5m])) / sum(rate(order_fill_rate_count[5m]))
```

## Order Book Metrics

### `orderbook_depth_levels`

**Type:** Gauge

**Labels:** `side` (`buy`, `sell`), `symbol`

**Description:** Number of price levels in the order book (bids or asks).

**Query Examples:**

```promql
# Current order book depth
orderbook_depth_levels{symbol="BTC-USDT", side="buy"}
```

### `orderbook_best_bid`

**Type:** Gauge

**Labels:** `symbol`

**Description:** Best bid price (highest buy price) in the order book.

### `orderbook_best_ask`

**Type:** Gauge

**Labels:** `symbol`

**Description:** Best ask price (lowest sell price) in the order book.

**Query Examples:**

```promql
# Spread in percentage
(orderbook_best_ask{symbol="BTC-USDT"} - orderbook_best_bid{symbol="BTC-USDT"}) / orderbook_best_bid{symbol="BTC-USDT"} * 100
```

## Matching Engine Metrics

### `matching_latency_seconds`

**Type:** Histogram

**Labels:** `operation` (`submit`, `cancel`), `symbol`

**Description:** Matching engine operation latency in seconds. Covers the time from order submission to result return.

**Query Examples:**

```promql
# P99 matching latency
histogram_quantile(0.99, rate(matching_latency_seconds_bucket[5m]))

# By operation type
histogram_quantile(0.95, rate(matching_latency_seconds_bucket{operation="submit"}[5m]))
```

### `matching_match_total`

**Type:** Counter

**Labels:** `side` (`buy`, `sell`), `symbol`

**Description:** Total number of matches (trades) produced by the matching engine.

**Query Examples:**

```promql
# Trades per second
rate(matching_match_total[1m])

# Trade value (requires quantity from logs)
```

## WAL Metrics

### `matching_wal_append_seconds`

**Type:** Histogram

**Description:** WAL (Write-Ahead Log) append latency in seconds. This metric is for the future WAL implementation.

**Query Examples:**

```promql
# WAL latency P99
histogram_quantile(0.99, rate(matching_wal_append_seconds_bucket[5m]))
```

## Rate Limiting Metrics

### `rate_limit_blocked_total`

**Type:** Counter

**Labels:** `scope` (`ip`, `user`, `api`), `identity` (IP address, user ID, or API path), `policy` (policy name)

**Description:** Total number of requests blocked by rate limiter.

**Query Examples:**

```promql
# Blocked requests per second by scope
rate(rate_limit_blocked_total[1m])

# Top blocked IPs
topk(10, sum by (identity) (rate(rate_limit_blocked_total{scope="ip"}[5m])))
```

### `rate_limit_requests_total`

**Type:** Counter

**Labels:** `scope`, `policy`

**Description:** Total number of rate-limited requests (both allowed and blocked).

### `rate_limit_errors_total`

**Type:** Counter

**Labels:** `scope`

**Description:** Total number of rate limit check errors (e.g., Redis connection failures).

## Saga Metrics

### `saga_state_transitions_total`

**Type:** Counter

**Labels:** `from` (previous state), `to` (next state)

**Description:** Total number of saga state transitions. This metric is for the future Saga implementation.

**Query Examples:**

```promql
# State transition rate by transition type
rate(saga_state_transitions_total[5m])
```

### `saga_retry_total`

**Type:** Counter

**Labels:** `step` (saga step name)

**Description:** Total number of saga step retries. This metric is for the future Saga implementation.

**Query Examples:**

```promql
# Retry rate per step
rate(saga_retry_total[5m])
```

## gRPC Metrics

### `grpc_server_requests_total`

**Type:** Counter

**Labels:** `service` (gRPC service name), `method` (method name), `code` (gRPC status code)

**Description:** Total number of gRPC server requests.

**Query Examples:**

```promql
# Requests per second
rate(grpc_server_requests_total[1m])

# Error rate
sum(rate(grpc_server_requests_total{code!="OK"}[5m])) / sum(rate(grpc_server_requests_total[5m]))
```

### `grpc_server_duration_seconds`

**Type:** Histogram

**Labels:** `service`, `method`

**Description:** gRPC server request duration in seconds.

**Query Examples:**

```promql
# P99 latency
histogram_quantile(0.99, rate(grpc_server_duration_seconds_bucket[5m]))
```

### `grpc_clients_requests_total`

**Type:** Counter

**Labels:** `client`, `method`, `status`

**Description:** Total number of gRPC client requests.

### `grpc_clients_failures_total`

**Type:** Counter

**Labels:** `client`, `method`, `error_type`

**Description:** Total number of gRPC client failures.

### `grpc_clients_circuit_state`

**Type:** Gauge

**Labels:** `client`

**Description:** Circuit breaker state for gRPC clients. Values: 0=closed, 1=open, 2=half-open.

## Distributed Tracing Metrics

### `trace_exporter_export_total`

**Type:** Counter

**Labels:** `result` (`success`, `failure`)

**Description:** Total number of trace export operations.

**Query Examples:**

```promql
# Export failure rate
sum(rate(trace_exporter_export_total{result="failure"}[5m])) / sum(rate(trace_exporter_export_total[5m]))
```

## Grafana Dashboard

For a visual dashboard, import the following PromQL queries into Grafana:

1. **Order Book Health:** `orderbook_depth_levels` gauges for bids and asks
2. **Order Flow:** `rate(order_created_total)` vs `rate(order_cancelled_total)`
3. **Matching Performance:** `histogram_quantile(0.99, rate(matching_latency_seconds_bucket[5m]))`
4. **Rate Limit Activity:** `rate(rate_limit_blocked_total)` and `rate(rate_limit_requests_total)`
5. **API Error Rate:** `sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))`

## Metric Cardinality Considerations

- `symbol` labels use the exchange's trading pair names (e.g., `BTC-USDT`, `ETH-USDT`). Keep the number of trading pairs reasonable to avoid cardinality explosion.
- `identity` in rate limiting includes IP addresses, which can be high cardinality. Consider aggregating by `/24` subnet in queries if needed.
- `path` in HTTP metrics should be normalized (avoid embedding user IDs or order IDs in paths).
