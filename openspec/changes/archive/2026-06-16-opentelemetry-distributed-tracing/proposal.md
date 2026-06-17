## Why

Distributed tracing is the observability foundation for debugging latency issues and understanding request flows across the exchange's microservices. With four services (gateway, user-svc, order-svc, matching-svc) now communicating over gRPC, correlating a user request through the entire stack — HTTP entry at the gateway → user auth → order creation → matching engine — is impossible without trace context propagation. This change adds OpenTelemetry instrumentation so every HTTP and gRPC call produces a span, with Jaeger as the local visualization backend.

## What Changes

- Add OpenTelemetry Go SDK and OTLP exporter dependencies to `go.mod`
- Create `pkg/tracing/tracing.go` with a single `Init(ctx, serviceName, otlpEndpoint)` function returning a shutdown func
- Wire `tracing.Init` into all four service entry points: `cmd/gateway/main.go`, `cmd/user-svc/main.go`, `cmd/order-svc/main.go`, `cmd/matching-svc/main.go`
- Add `otelgin` middleware to `internal/gateway/router/router.go` for HTTP span creation
- Wire `otelgrpc.NewServerHandler()` as a `grpc.StatsHandler` into all gRPC servers via `grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()), ...)`
- Wire `otelgrpc.NewClientHandler()` into gRPC clients via `grpc.WithStatsHandler(otelgrpc.NewClientHandler())` — affects `internal/gateway/client/clients.go` and `internal/matching/client/order_client.go`
- Link `x-request-id` (already propagated via `pkg/grpcx`) to trace spans as a `request.id` span attribute
- Add `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME` environment variables to docker-compose for all services
- Add `jaeger` service to `docker-compose.yml` using `jaegertracing/all-in-one`
- Optionally add `deploy/otel-collector.yaml` as a standalone OTel Collector deployment manifest

## Capabilities

### New Capabilities

- `distributed-tracing`: Covers the entire instrumentation pipeline — SDK initialization, HTTP/gRPC auto-instrumentation, request-id → trace linking, OTLP export, and Jaeger UI smoke test. This is a cross-cutting infrastructure capability rather than a domain feature, so it does not map to a single bounded spec. The acceptance criteria live in tasks.md.

### Modified Capabilities

- No existing spec requirements are changed. This is purely infrastructure work that must be completed before the observability story can be validated.

## Impact

- **New dependencies**: `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/sdk/trace`, `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`, `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc`, `go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin`
- **Modified files**: All four `cmd/*/main.go` files (tracing init + graceful shutdown), `internal/gateway/router/router.go` (otelgin), `internal/gateway/client/clients.go` (otelgrpc client stats handler), `internal/matching/client/order_client.go` (otelgrpc client stats handler), `docker-compose.yml` (jaeger service + env vars)
- **New files**: `pkg/tracing/tracing.go`, `deploy/otel-collector.yaml` (optional)
- **Services**: gateway, user-svc, order-svc, matching-svc all gain tracing spans
- **Out of scope**: metrics (Prometheus already present), structured logging correlation, alerting, OTel Collector production deployment
