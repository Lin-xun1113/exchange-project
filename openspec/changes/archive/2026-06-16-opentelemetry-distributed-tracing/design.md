## Context

The exchange-project currently has no distributed tracing. With four services communicating over gRPC (gateway, user-svc, order-svc, matching-svc), debugging latency issues or understanding request correlations requires manual log grepping across four log streams. The `pkg/grpcx` package already propagates `x-request-id` via gRPC metadata, but there is no way to visualize the causal chain of a request across service boundaries.

The request-id propagation work (already merged) provides the correlation key; this design uses it as the bridge between trace spans and existing logs.

## Goals / Non-Goals

**Goals:**

- Every HTTP request entering the gateway produces an HTTP span via `otelgin`
- Every gRPC call (client ↔ server) produces a span via `otelgrpc`
- All spans share a trace context propagated via W3C TraceContext headers over gRPC metadata
- The `x-request-id` value is attached to every span as attribute `request.id`, enabling correlation with existing structured logs
- Traces are exported to Jaeger via OTLP (either directly or via an optional OTel Collector)
- `go build ./...` and `go test ./...` pass after the change

**Non-Goals:**

- Metrics (Prometheus instrumentation already exists; adding OTel metrics is a separate ROADMAP item)
- Structured log field injection (trace ID in log lines; separate work item)
- Production OTel Collector deployment (local dev only via docker-compose)
- Automatic context propagation for background workers / message queues (out of scope for this change)
- Backward trace context injection into existing logs (not needed for acceptance criteria)

## Decisions

### Decision 1: OTLP over stdout exporter for development

**Choice**: Use `otlptracegrpc` exporter pointing to `http://jaeger:4317` in docker-compose, with a stdout fallback for local dev outside Docker.

**Rationale**: The OTLP protocol is the OTel SDK's native wire format and is the recommended production path. Jaeger's all-in-one image accepts OTLP directly on port 4317, eliminating the need for a separate Collector in the local dev loop.

**Alternative considered — stdout exporter**: Writing traces to stdout is useful for debugging but does not integrate with Jaeger UI. Not acceptable for the acceptance criteria.

**Alternative considered — Jaeger exporter**: The Jaeger exporter (`jaeger-client-go`) is deprecated in favor of OTLP. Using it would require maintaining an unsupported path.

### Decision 2: Parent-based always-sample sampler

**Choice**: `trace.ParentBased(trace.AlwaysSample())`.

**Rationale**: Services are always started in a context where a parent span exists (either from the gateway for downstream calls, or from an incoming HTTP request). The `ParentBased` sampler means:
- If there is a parent span, follow its sampling decision (preserves context from upstream)
- If there is no parent span (e.g., direct service startup), always sample

This avoids oversampling in production while ensuring development traces are always captured.

### Decision 3: `grpc.StatsHandler` for gRPC instrumentation

**Choice**: Pass `otelgrpc.NewServerHandler()` / `otelgrpc.NewClientHandler()` as `grpc.StatsHandler` via `grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()), ...)` and `grpc.Dial(..., grpc.WithStatsHandler(otelgrpc.NewClientHandler()), ...)`.

**Rationale**: `StatsHandler` is the idiomatic gRPC interception point in v1.6+ and operates at the connection level, correctly propagating W3C TraceContext metadata. This avoids the overhead of chaining another unary interceptor via `grpc.ChainUnaryInterceptor`, and works correctly for streaming RPCs as well.

### Decision 4: `pkg/tracing` as a shared package

**Choice**: New file `pkg/tracing/tracing.go` with `Init(ctx, serviceName, otlpEndpoint) (shutdown func(context.Context) error, error)`.

**Rationale**: All four services need the same initialization sequence. A shared package avoids duplicating the exporter, sampler, and resource setup in each `main.go`. The shutdown func pattern ensures graceful flush on `SIGINT` — critical for ensuring in-flight spans are exported before the process exits.

### Decision 5: `request.id` span attribute via modified `grpcx.UnaryServerRequestID`

**Choice**: Extend `UnaryServerRequestID` to call `oteltrace.SpanFromContext(ctx).SetAttributes(oteltrace.WithString("request.id", requestID))`.

**Rationale**: The request ID is already extracted by `UnaryServerRequestID`. Modifying it to also annotate the span is a minimal, zero-overhead addition. An alternative (custom span processor) was considered but is overkill since the request ID is available at the interceptor level.

### Decision 6: No OTel Collector in docker-compose by default

**Choice**: Services export OTLP directly to Jaeger. Add `deploy/otel-collector.yaml` as an optional manifest.

**Rationale**: The Jaeger all-in-one image accepts OTLP directly on port 4317, so a separate Collector adds latency and complexity for no benefit in local development. The optional `deploy/otel-collector.yaml` covers teams that want to experiment with Collector-based routing (e.g., fan-out to multiple backends) without making it a prerequisite.

## Risks / Trade-offs

[Risk] OTLP endpoint unavailable at startup → **Mitigation**: The `Init` func returns an error if the exporter fails to start. Services should fail fast rather than run without tracing. In docker-compose, Jaeger is listed as a `depends_on` service, but no health check is enforced — services may start before Jaeger is ready. Document that traces from early startup may be dropped. Consider adding a retry loop with backoff in `pkg/tracing` for production use.

[Risk] Trace context not propagating for non-gRPC calls (e.g., direct DB access, Redis calls) → **Mitigation**: Auto-instrumentation for database drivers and Redis client is out of scope for this change (requires additional OTel packages). Document this as a follow-up item.

[Risk] Shutdown ordering: `tracing.Shutdown` must be called before the process exits to flush remaining spans. The shutdown func returned by `Init` must be called in each `main.go`'s graceful shutdown sequence, after `grpcServer.GracefulStop()` / `srv.Shutdown()` but before `logger.Sync()`. This ordering must be documented clearly in the tasks.

[Risk] Jaeger UI not loading traces due to CORS → **Mitigation**: Jaeger all-in-one has `--query.base-path` and CORS flags. The docker-compose service exposes port 16686; local browsers should work without additional config. If issues arise, add `--collector.grpc-server.tls.enabled=false` or set `JAEGER_DISABLED=true` for the UI.

## Migration Plan

1. **Additive only** — this change introduces new dependencies but does not modify existing gRPC method signatures or HTTP APIs.
2. **Order of operations**:
   - Add OTel Go dependencies (`go get`) — non-breaking
   - Create `pkg/tracing/tracing.go` — new file, no existing code affected
   - Modify each `cmd/*/main.go` to call `tracing.Init` — 5-line addition per service
   - Modify `internal/gateway/router/router.go` — add `r.Use(otelgin.Middleware(cfg.App.Name))`
   - Modify gRPC server creation in all services — add `grpc.StatsHandler(otelgrpc.NewServerHandler())`
   - Modify gRPC client creation in `internal/gateway/client/clients.go` and `internal/matching/client/order_client.go` — add `grpc.WithStatsHandler(otelgrpc.NewClientHandler())`
   - Modify `pkg/grpcx/interceptor.go` — add OTel span attribute line in `UnaryServerRequestID`
   - Update docker-compose: add Jaeger service + OTEL env vars
   - Verify: `go build ./...` → `go test ./...` → `docker-compose up` → smoke test Jaeger UI
3. **Rollback**: Revert the five modified `cmd/*/main.go` files, `router.go`, `clients.go`, `interceptor.go`, and docker-compose. Run `go mod tidy` to remove OTel dependencies. No data migration needed.

## Open Questions

1. Should the OTel SDK be initialized before or after the logger? Currently logger is initialized first in all `main.go` files. Tracing initialization could log errors via the already-initialized logger. Decision: tracing after logger is fine — it can use `logger.Warn` for non-fatal init failures.

2. Should `OTEL_SERVICE_NAME` default to the binary name (e.g., `gateway`, `user-svc`) or be a configurable value? Decision: default to `os.Args[0]` basename, overridable via env var. All four services share the same config struct, so adding a `Tracing.ServiceName` field is straightforward if needed later.

3. Should span attributes include `service.version` from the binary? The OTel Go SDK supports `semconv.ServiceVersion` attribute. Decision: skip for now, add as a follow-up when version info is available from build ldflags.
