# Tasks: metrics-expansion

## 1. Metrics Registration (pkg/metrics/metrics.go)

- [x] 1.1 Register `order_created_total{side, symbol}` Counter
- [x] 1.2 Register `order_cancelled_total{side, symbol}` Counter
- [x] 1.3 Register `order_fill_rate{side, symbol}` Histogram
- [x] 1.4 Register `orderbook_depth_levels{side, symbol}` Gauge
- [x] 1.5 Register `orderbook_best_bid{symbol}` Gauge
- [x] 1.6 Register `orderbook_best_ask{symbol}` Gauge
- [x] 1.7 Register `matching_latency_seconds{operation, symbol}` Histogram
- [x] 1.8 Register `matching_match_total{side, symbol}` Counter
- [x] 1.9 Register `matching_wal_append_seconds` Histogram (placeholder for future WAL)
- [x] 1.10 Register `rate_limit_blocked_total{scope, identity}` Counter
- [x] 1.11 Register `saga_state_transitions_total{from, to}` Counter (placeholder for future Saga)
- [x] 1.12 Register `saga_retry_total{step}` Counter (placeholder for future Saga)
- [x] 1.13 Register `grpc_server_requests_total{service, method, code}` Counter
- [x] 1.14 Register `grpc_server_duration_seconds{service, method}` Histogram
- [x] 1.15 Register `trace_exporter_export_total{result}` Counter
- [x] 1.16 Add helper methods for all new metric types

## 2. Order Service Metrics (internal/order/service/order_service.go)

- [x] 2.1 Record `order_created_total` on `CreateOrder` success
- [x] 2.2 Record `order_cancelled_total` on `CancelOrder` success
- [x] 2.3 Expose method to record `order_fill_rate` after matching

## 3. Matching Engine Metrics (internal/matching/engine/engine.go)

- [x] 3.1 Record `matching_latency_seconds` in `SubmitOrder` with operation="submit"
- [x] 3.2 Record `matching_latency_seconds` in `CancelOrder` with operation="cancel"
- [x] 3.3 Record `matching_match_total` when trades are produced
- [x] 3.4 Record `orderbook_depth_levels` and best bid/ask on `GetOrderBook`/`GetBestPrice` calls

## 4. Rate Limiting Metrics (internal/gateway/middleware/middleware.go)

- [x] 4.1 Record `rate_limit_blocked_total{scope="ip"}` when RateLimit blocks a request

## 5. Documentation (docs/metrics.md)

- [x] 5.1 Write `docs/metrics.md` explaining each metric's purpose and query examples

## 6. Verification

- [x] 6.1 Verify `go build ./...` passes (pkg/metrics, order service, matching engine, gateway middleware)
- [x] 6.2 Verify all metrics are registered at startup
