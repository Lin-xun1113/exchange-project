## 1. Dependencies

- [x] 1.1 Add OTel Go SDK and OTLP trace exporter: `go get go.opentelemetry.io/otel go.opentelemetry.io/otel/sdk go.opentelemetry.io/otel/sdk/trace go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`
- [x] 1.2 Add OTel gRPC instrumentation: `go get go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc`
- [x] 1.3 Add OTel Gin instrumentation: `go get go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin`
- [x] 1.4 Run `go mod tidy` to clean up dependency graph

## 2. Tracing package

- [x] 2.1 Create `pkg/tracing/tracing.go` with `Init(ctx, serviceName, otlpEndpoint) (shutdown func(context.Context) error, error)` using `otlptracegrpc`, `trace.ParentBased(trace.AlwaysSample())` sampler, and graceful shutdown
- [x] 2.2 Verify `pkg/tracing/tracing.go` compiles: `go build ./pkg/tracing/...`

## 3. Gateway HTTP instrumentation

- [x] 3.1 Import `go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin` in `internal/gateway/router/router.go`
- [x] 3.2 Add `otelgin.Middleware(serviceName)` to the global Gin middleware chain in `router.Setup()` (after `middleware.RequestID()`)
- [x] 3.3 Verify gateway builds: `go build ./cmd/gateway/...`

## 4. gRPC server instrumentation

- [x] 4.1 Import `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc` in `cmd/gateway/main.go`
- [x] 4.2 Wire `otelgrpc.NewServerHandler()` into `client.NewClients` — modify `internal/gateway/client/clients.go` to pass `grpc.WithStatsHandler(otelgrpc.NewClientHandler())` to each `grpc.Dial` call
- [x] 4.3 Wire `otelgrpc.NewServerHandler()` into `cmd/user-svc/main.go`: add `grpc.StatsHandler(otelgrpc.NewServerHandler())` to `grpc.NewServer(...)`
- [x] 4.4 Wire `otelgrpc.NewServerHandler()` into `cmd/order-svc/main.go`: add `grpc.StatsHandler(otelgrpc.NewServerHandler())` to `grpc.NewServer(...)`
- [x] 4.5 Wire `otelgrpc.NewServerHandler()` into `cmd/matching-svc/main.go`: add `grpc.StatsHandler(otelgrpc.NewServerHandler())` to `grpc.NewServer(...)`
- [x] 4.6 Verify all services build: `go build ./cmd/...`

## 5. Tracing Init and graceful shutdown

- [x] 5.1 Add `tracing.Init(ctx, "gateway", otelEndpoint)` to `cmd/gateway/main.go`, call shutdown in the `srv.Shutdown` defer chain
- [x] 5.2 Add `tracing.Init(ctx, "user-svc", otelEndpoint)` to `cmd/user-svc/main.go`, call shutdown before `grpcServer.GracefulStop()`
- [x] 5.3 Add `tracing.Init(ctx, "order-svc", otelEndpoint)` to `cmd/order-svc/main.go`, call shutdown before `grpcServer.GracefulStop()`
- [x] 5.4 Add `tracing.Init(ctx, "matching-svc", otelEndpoint)` to `cmd/matching-svc/main.go`, call shutdown before `grpcServer.GracefulStop()`
- [x] 5.5 Verify all services build after shutdown wiring: `go build ./...`

## 6. Request ID → Trace ID linkage

- [x] 6.1 Modify `pkg/grpcx/interceptor.go`: in `UnaryServerRequestID`, after extracting the request ID from metadata, add `oteltrace.SpanFromContext(ctx).SetAttributes(oteltrace.WithString("request.id", vals[0]))` (requires importing `go.opentelemetry.io/otel/trace`)
- [x] 6.2 Verify `pkg/grpcx` compiles: `go build ./pkg/grpcx/...`

## 7. Matching service gRPC client instrumentation

- [x] 7.1 Import `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc` in `internal/matching/client/order_client.go`
- [x] 7.2 Add `grpc.WithStatsHandler(otelgrpc.NewClientHandler())` to `grpc.Dial` call in `NewOrderClient`
- [x] 7.3 Verify matching client builds: `go build ./internal/matching/...`

## 8. Configuration and environment

- [x] 8.1 Add `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME` env vars to the gateway, user-svc, order-svc, and matching-svc service blocks in `docker-compose.yml`
- [x] 8.2 Add `jaeger` service to `docker-compose.yml` using image `jaegertracing/all-in-one:1.52`, exposing ports `6831/udp` (Jaeger agent), `16686` (UI), `4317` (OTLP gRPC)
- [x] 8.3 Create `deploy/otel-collector.yaml` (optional): minimal OTel Collector config that receives OTLP and forwards to Jaeger, for teams that want Collector-based routing
- [x] 8.4 Run `docker-compose config` to validate the updated compose file

## 9. Tests and verification

- [x] 9.1 Run `go build ./...` to ensure all packages compile
- [x] 9.2 Run `go test ./...` to ensure existing tests pass
- [x] 9.3 Write a smoke test: `smoke/tracing_test.go` that starts jaeger, makes an order creation request via the gateway, waits 2s, then queries `http://jaeger:16686/api/traces` to verify the trace appears
- [x] 9.4 Run smoke test: `go test ./smoke/... -v`
