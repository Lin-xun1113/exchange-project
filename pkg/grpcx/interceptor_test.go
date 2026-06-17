package grpcx

import (
	"context"
	"testing"

	"github.com/linxun2025/exchange-project/pkg/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func init() {
	logger.Init("test")
}

func TestUnaryClientRequestID_WithRequestID(t *testing.T) {
	interceptor := UnaryClientRequestID()

	var capturedCtx context.Context
	capturedMethod := ""

	invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		capturedCtx = ctx
		capturedMethod = method
		return nil
	}

	ctx := logger.NewContextWithRequestID(context.Background())

	interceptor(ctx, "/test.Service/Method", nil, nil, nil, invoker)

	if capturedMethod != "/test.Service/Method" {
		t.Errorf("expected method /test.Service/Method, got %s", capturedMethod)
	}

	md, ok := metadata.FromOutgoingContext(capturedCtx)
	if !ok {
		t.Fatal("expected metadata in outgoing context")
	}

	vals := md.Get(RequestIDMetadataKey)
	if len(vals) == 0 {
		t.Error("expected x-request-id in outgoing metadata")
	} else if vals[0] == "" {
		t.Error("expected non-empty request ID")
	}
}

func TestUnaryClientRequestID_WithoutRequestID(t *testing.T) {
	interceptor := UnaryClientRequestID()

	var capturedCtx context.Context

	invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		capturedCtx = ctx
		return nil
	}

	ctx := context.Background()

	err := interceptor(ctx, "/test.Service/Method", nil, nil, nil, invoker)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	_, ok := metadata.FromOutgoingContext(capturedCtx)
	if !ok {
		t.Log("no outgoing metadata (expected when no request ID)")
	}
}

func TestUnaryServerRequestID_WithRequestID(t *testing.T) {
	interceptor := UnaryServerRequestID()

	requestID := "test-request-id-12345"

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(RequestIDMetadataKey, requestID))

	var capturedCtx context.Context
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		capturedCtx = ctx
		return nil, nil
	}

	_, _ = interceptor(ctx, nil, &grpc.UnaryServerInfo{}, handler)

	got := logger.GetRequestID(capturedCtx)
	if got != requestID {
		t.Errorf("expected request ID %q, got %q", requestID, got)
	}
}

func TestUnaryServerRequestID_WithoutRequestID(t *testing.T) {
	interceptor := UnaryServerRequestID()

	ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{}))

	var capturedCtx context.Context
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		capturedCtx = ctx
		return nil, nil
	}

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, handler)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	got := logger.GetRequestID(capturedCtx)
	if got != "" {
		t.Errorf("expected empty request ID, got %q", got)
	}
}

func TestUnaryServerRequestID_CaseInsensitiveMetadata(t *testing.T) {
	interceptor := UnaryServerRequestID()

	requestID := "abc-upper"
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("X-Request-ID", requestID))

	var capturedCtx context.Context
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		capturedCtx = ctx
		return nil, nil
	}

	_, _ = interceptor(ctx, nil, &grpc.UnaryServerInfo{}, handler)

	got := logger.GetRequestID(capturedCtx)
	if got != requestID {
		t.Errorf("expected request ID %q (case-insensitive metadata), got %q", requestID, got)
	}
}

func TestUnaryServerRequestID_EmptyValue(t *testing.T) {
	interceptor := UnaryServerRequestID()

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(RequestIDMetadataKey, ""))

	var capturedCtx context.Context
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		capturedCtx = ctx
		return nil, nil
	}

	_, _ = interceptor(ctx, nil, &grpc.UnaryServerInfo{}, handler)

	// Documents the current behavior: empty value overrides ctx with "".
	// If we want to preserve upstream request ID, we'd change interceptor logic.
	got := logger.GetRequestID(capturedCtx)
	t.Logf("captured request_id with empty metadata value: %q", got)
}
