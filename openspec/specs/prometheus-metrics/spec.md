# prometheus-metrics

覆盖交易系统全链路的 Prometheus 指标暴露能力。

## Requirements

### Requirement: Order Lifecycle Metrics
The system SHALL expose order lifecycle metrics for monitoring order flow and fill rates.

#### Scenario: Order created increments counter
- **WHEN** an order is successfully created via `OrderService.CreateOrder`
- **THEN** `order_created_total{side=<side>, symbol=<symbol>}` is incremented by 1

#### Scenario: Order cancelled increments counter
- **WHEN** an order is successfully cancelled via `OrderService.CancelOrder`
- **THEN** `order_cancelled_total{side=<side>, symbol=<symbol>}` is incremented by 1

#### Scenario: Order fill rate recorded
- **WHEN** an order match result is processed with trades
- **THEN** `order_fill_rate{side=<side>, symbol=<symbol>}` observes the ratio of filled quantity to original quantity

### Requirement: Order Book Metrics
The system SHALL expose order book depth and best price metrics for market analysis.

#### Scenario: Order book depth levels exposed
- **WHEN** `Matcher.GetOrderBook` is called
- **THEN** `orderbook_depth_levels{side=buy, symbol=<symbol>}` reflects the number of bid levels
- **AND** `orderbook_depth_levels{side=sell, symbol=<symbol>}` reflects the number of ask levels

#### Scenario: Best bid/ask prices exposed
- **WHEN** `Matcher.GetBestPrice` is called
- **THEN** `orderbook_best_bid{symbol=<symbol>}` reflects the highest bid price as a gauge
- **AND** `orderbook_best_ask{symbol=<symbol>}` reflects the lowest ask price as a gauge

### Requirement: Matching Engine Metrics
The system SHALL expose matching engine latency and throughput metrics.

#### Scenario: Matching latency recorded per operation
- **WHEN** a matching operation (submit/cancel) completes
- **THEN** `matching_latency_seconds{operation=<submit|cancel>, symbol=<symbol>}` observes the elapsed time

#### Scenario: Matching trades counted
- **WHEN** a match produces trades
- **THEN** `matching_match_total{side=<buy|sell>, symbol=<symbol>}` is incremented by the number of trades

### Requirement: WAL Metrics
The system SHALL expose Write-Ahead Log append latency metrics (for future WAL implementation).

#### Scenario: WAL append latency recorded
- **WHEN** a WAL append operation completes (after WAL is implemented)
- **THEN** `matching_wal_append_seconds` observes the append duration

### Requirement: Rate Limiting Metrics
The system SHALL expose rate limiting blocked request counts.

#### Scenario: Rate limited requests counted
- **WHEN** a request is blocked by rate limiter
- **THEN** `rate_limit_blocked_total{scope=<ip|user|api>, identity=<identifier>}` is incremented by 1

### Requirement: Saga Metrics
The system SHALL expose saga state machine transition and retry metrics (for future Saga implementation).

#### Scenario: Saga state transitions counted
- **WHEN** a saga transitions from one state to another (after Saga is implemented)
- **THEN** `saga_state_transitions_total{from=<prev_state>, to=<next_state>}` is incremented by 1

#### Scenario: Saga retries counted
- **WHEN** a saga step retry occurs (after Saga is implemented)
- **THEN** `saga_retry_total{step=<step_name>}` is incremented by 1

### Requirement: gRPC Server Metrics
The system SHALL expose gRPC server request counts and durations.

#### Scenario: gRPC server request counted
- **WHEN** a gRPC server request completes
- **THEN** `grpc_server_requests_total{service=<svc>, method=<method>, code=<status_code>}` is incremented by 1

#### Scenario: gRPC server duration recorded
- **WHEN** a gRPC server request completes
- **THEN** `grpc_server_duration_seconds{service=<svc>, method=<method>}` observes the request duration

### Requirement: Distributed Tracing Metrics
The system SHALL expose trace exporter export results.

#### Scenario: Trace export result recorded
- **WHEN** a trace export operation completes
- **THEN** `trace_exporter_export_total{result=<success|failure>}` is incremented by 1
