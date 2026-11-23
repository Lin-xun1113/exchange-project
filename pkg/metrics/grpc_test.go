package metrics

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCClientInterceptorsShareRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptors := []grpc.UnaryClientInterceptor{GRPCClientInterceptor(reg), GRPCClientInterceptor(reg)}
	ctx := context.Background()
	method := "/test.Service/Method"
	for i, intercept := range interceptors {
		for _, code := range []codes.Code{codes.OK, codes.Unavailable} {
			invoke := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				return status.Error(code, "upstream")
			}
			if got := status.Code(intercept(ctx, method, nil, nil, nil, invoke)); got != code {
				t.Fatalf("interceptor %d: returned %s, want %s", i, got, code)
			}
		}
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 2 {
		t.Fatalf("got %d metric families, want 2", len(families))
	}
	for _, family := range families {
		switch family.GetName() {
		case "grpc_client_handled_total":
			points := family.GetMetric()
			if len(points) != 2 {
				t.Fatalf("got %d counter series, want one for each status code", len(points))
			}
			for _, point := range points {
				if n := point.GetCounter().GetValue(); n != 2 {
					t.Errorf("labels %v: got %v calls, want 2", point.GetLabel(), n)
				}
			}
		case "grpc_client_handling_seconds":
			points := family.GetMetric()
			if len(points) != 1 || points[0].GetHistogram().GetSampleCount() != 4 {
				t.Errorf("want a single duration series with four samples, got %v", points)
			}
		default:
			t.Errorf("unexpected metric family %s", family.GetName())
		}
	}
}
