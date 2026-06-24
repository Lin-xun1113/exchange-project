// Package tracing provides OpenTelemetry initialization for the exchange services.
package tracing

import (
	"context"
	"fmt"
	"os"

	"github.com/linxun2025/exchange-project/pkg/metrics"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// sdktrace.Span is the interface we need
type metricsExporter struct {
	delegate sdktrace.SpanExporter
}

// ExportSpans wraps the delegate exporter and records metrics
func (e *metricsExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.delegate.ExportSpans(ctx, spans)
	if err != nil {
		metrics.GetMetrics().RecordTraceExporterExport("error")
		return err
	}
	metrics.GetMetrics().RecordTraceExporterExport("success")
	return nil
}

// Shutdown delegates to the underlying exporter
func (e *metricsExporter) Shutdown(ctx context.Context) error {
	return e.delegate.Shutdown(ctx)
}

// Init initializes the OpenTelemetry SDK with an OTLP gRPC exporter and a
// ParentBased(AlwaysSample) sampler. It returns a shutdown function that must
// be called on service exit to flush in-flight spans.
func Init(ctx context.Context, serviceName, otlpEndpoint string) (shutdown func(context.Context) error, rerr error) {
	if otlpEndpoint == "" {
		otlpEndpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if otlpEndpoint == "" {
		otlpEndpoint = "http://localhost:4317"
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otlpEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Wrap the exporter to record metrics
	metricsExporter := &metricsExporter{delegate: exporter}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(metricsExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		err := tp.Shutdown(ctx)
		if err != nil {
			return fmt.Errorf("failed to shutdown tracer provider: %w", err)
		}
		return exporter.Shutdown(ctx)
	}, nil
}
