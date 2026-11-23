package grpcx

import (
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const (
	maxConcurrentStreams = 100
	maxMessageSize       = 4 * 1024 * 1024
	bufferSize           = 32 * 1024
)

// NewServer configures a gRPC server with health and reflection. Callers mark
// the empty service name SERVING after their dependencies are ready.
func NewServer(reg prometheus.Registerer) (*grpc.Server, *health.Server) {
	srv := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(UnaryServerRequestID(), metrics.GRPCServerInterceptor(reg)),
		grpc.MaxConcurrentStreams(maxConcurrentStreams),
		grpc.MaxRecvMsgSize(maxMessageSize),
		grpc.MaxSendMsgSize(maxMessageSize),
		grpc.ReadBufferSize(bufferSize),
		grpc.WriteBufferSize(bufferSize),
	)
	hs := health.NewServer()
	hs.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	grpc_health_v1.RegisterHealthServer(srv, hs)
	reflection.Register(srv)
	return srv, hs
}
