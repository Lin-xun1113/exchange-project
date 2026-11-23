package metrics

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// GRPCServerInterceptor records unary RPCs handled by the service.
func GRPCServerInterceptor(reg prometheus.Registerer) grpc.UnaryServerInterceptor {
	m := newRPCMetrics(reg, "grpc_server")
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		response, err := handler(ctx, req)
		m.observe(info.FullMethod, status.Code(err).String(), time.Since(start))
		return response, err
	}
}

// GRPCClientInterceptor records unary RPC attempts. Interceptors for different
// connections can share the same registry and accumulate into the same series.
func GRPCClientInterceptor(reg prometheus.Registerer) grpc.UnaryClientInterceptor {
	m := newRPCMetrics(reg, "grpc_client")
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()
		err := invoke(ctx, method, req, reply, cc, opts...)
		m.observe(method, status.Code(err).String(), time.Since(start))
		return err
	}
}

type rpcMetrics struct {
	handled  *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newRPCMetrics(reg prometheus.Registerer, subsystem string) rpcMetrics {
	handled := prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: subsystem,
		Name:      "handled_total",
		Help:      "Completed unary RPCs by method and gRPC status code.",
	}, []string{"method", "code"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: subsystem,
		Name:      "handling_seconds",
		Help:      "Duration of unary RPCs in seconds.",
	}, []string{"method"})
	return rpcMetrics{
		handled:  registerOrReuse(reg, handled),
		duration: registerOrReuse(reg, duration),
	}
}

func (m rpcMetrics) observe(method, code string, d time.Duration) {
	m.handled.WithLabelValues(method, code).Inc()
	m.duration.WithLabelValues(method).Observe(d.Seconds())
}

func registerOrReuse[C prometheus.Collector](reg prometheus.Registerer, c C) C {
	if err := reg.Register(c); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			if existing, ok := already.ExistingCollector.(C); ok {
				return existing
			}
		}
		panic(err)
	}
	return c
}
