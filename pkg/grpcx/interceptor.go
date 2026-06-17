// Package grpcx provides shared gRPC utilities including interceptors.
package grpcx

import (
	"context"

	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// RequestIDMetadataKey is the gRPC metadata key for request ID propagation.
// Uses lowercase per gRPC convention.
const RequestIDMetadataKey = "x-request-id"

// spanRequestIDKey is the attribute key used when tagging spans with request IDs.
const spanRequestIDKey = "request.id"

// UnaryClientRequestID returns a client interceptor that propagates the request ID
// from the context to outgoing gRPC metadata.
func UnaryClientRequestID() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req interface{},
		reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if requestID := logger.GetRequestID(ctx); requestID != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, RequestIDMetadataKey, requestID)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryServerRequestID returns a server interceptor that extracts the request ID
// from incoming gRPC metadata and puts it into the context.
func UnaryServerRequestID() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if vals := md.Get(RequestIDMetadataKey); len(vals) > 0 {
				ctx = logger.WithRequestID(ctx, vals[0])
				if span := trace.SpanFromContext(ctx); span.IsRecording() {
					span.SetAttributes(attribute.String(spanRequestIDKey, vals[0]))
				}
			}
		}
		return handler(ctx, req)
	}
}
