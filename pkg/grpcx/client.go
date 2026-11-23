package grpcx

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/balancer/roundrobin" // Register the configured client policy.
	"google.golang.org/grpc/credentials/insecure"
)

// Dial creates a gRPC client connection for internal service traffic.
func Dial(target string, extra ...grpc.UnaryClientInterceptor) (*grpc.ClientConn, error) {
	return grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithUnaryInterceptor(UnaryClientRequestID()),
		grpc.WithChainUnaryInterceptor(extra...),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`),
	)
}
