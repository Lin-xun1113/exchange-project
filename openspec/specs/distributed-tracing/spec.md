# Distributed Tracing

## Purpose

Provides cross-service distributed tracing via OpenTelemetry, enabling end-to-end visibility into request flows across the exchange's microservices (gateway, user-svc, order-svc, matching-svc).

## Requirements

### Requirement: Tracing SDK initialization

The system SHALL provide a shared tracing package (`pkg/tracing`) that initializes the OpenTelemetry SDK with an OTLP exporter and a parent-based always-sample sampler, returning a shutdown function that flushes in-flight spans.

#### Scenario: Successful initialization
- **WHEN** `tracing.Init(ctx, "gateway", "http://jaeger:4317")` is called in a running service
- **THEN** the function returns a non-nil shutdown func and nil error, and a trace provider is registered globally

#### Scenario: Init with invalid endpoint
- **WHEN** `tracing.Init(ctx, "gateway", "invalid://endpoint")` is called
- **THEN** the function returns a nil shutdown func and a non-nil error, and the program may proceed without tracing

#### Scenario: Shutdown flushes spans
- **WHEN** the shutdown func returned by `tracing.Init` is called with a context
- **THEN** all pending spans are exported to the configured OTLP endpoint before the function returns nil

### Requirement: HTTP request spans

The system SHALL create a trace span for every HTTP request entering the API Gateway, instrumented via the `otelgin` middleware.

#### Scenario: Gateway HTTP request produces a span
- **WHEN** an HTTP POST request is made to `/api/v1/auth/login` on the gateway
- **THEN** a span named `POST /api/v1/auth/login` is created under the current trace, with `span.kind = server`, `http.method = POST`, `http.url`, and `http.status_code` attributes

#### Scenario: Span includes request ID
- **WHEN** an HTTP request with header `x-request-id: abc123` arrives at the gateway
- **THEN** the resulting HTTP span has attribute `request.id = "abc123"`

### Requirement: gRPC server spans

The system SHALL create a trace span for every gRPC unary and streaming RPC handled by each microservice, via `otelgrpc.NewServerHandler()` registered as a `grpc.StatsHandler`.

#### Scenario: User-svc RPC produces a span
- **WHEN** a gRPC `GetUser` call is received by `user-svc`
- **THEN** a span named `proto.UserService/GetUser` is created with `span.kind = server`, `rpc.method`, `rpc.service`, and `rpc.system = grpc` attributes

#### Scenario: Server span includes request ID
- **WHEN** a gRPC call arrives with metadata `x-request-id: abc123`
- **THEN** the server span has attribute `request.id = "abc123"`

### Requirement: gRPC client spans

The system SHALL create a trace span for every outbound gRPC call made by the gateway and the matching service, via `otelgrpc.NewClientHandler()` registered as a `grpc.WithStatsHandler` dial option.

#### Scenario: Gateway → User-svc call produces a client span
- **WHEN** the gateway's user client calls `GetUser` on `user-svc`
- **THEN** a span named `proto.UserService/GetUser` is created with `span.kind = client`, linked to the parent HTTP span via shared trace context

#### Scenario: Matching-svc → Order-svc call produces a client span
- **WHEN** `matching-svc` calls `UpdateOrder` on `order-svc`
- **THEN** a client span is created under the current trace context, with `span.kind = client`

### Requirement: Trace context propagation

The system SHALL propagate W3C TraceContext headers (traceparent, tracestate) over gRPC metadata between all services, so that a single user request produces one continuous trace across gateway → user-svc → order-svc → matching-svc.

#### Scenario: Trace continuity across four services
- **WHEN** a user creates an order via the gateway (HTTP) which calls order-svc (gRPC) which calls matching-svc (gRPC)
- **THEN** Jaeger displays a single trace with four spans, all sharing the same trace ID

### Requirement: Jaeger visualization

The system SHALL provide a local Jaeger all-in-one instance via docker-compose, accessible at `http://localhost:16686`, that receives OTLP traces from all services.

#### Scenario: Smoke test finds trace in Jaeger UI
- **WHEN** `docker-compose up` is run and the smoke test makes an order creation request
- **THEN** the Jaeger UI at port 16686 shows the trace for that request under the `gateway` service, with child spans visible for `order-svc` and `matching-svc`
