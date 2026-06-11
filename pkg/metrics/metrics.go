// Package metrics 提供 Prometheus 指标收集
package metrics

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	once     sync.Once
	instance *Metrics

	// HTTP metrics
	httpRequestsTotal *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	httpRequestsInFlight int64

	// gRPC metrics
	grpcRequestsTotal *prometheus.CounterVec
	grpcRequestDuration *prometheus.HistogramVec
)

// Metrics 指标收集器
type Metrics struct {
	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	httpRequestsInFlight int64

	grpcRequestsTotal   *prometheus.CounterVec
	grpcRequestDuration *prometheus.HistogramVec

	grpcClientsTotal       *prometheus.CounterVec
	grpcClientFailuresTotal *prometheus.CounterVec
	grpcClientCircuitState *prometheus.GaugeVec
}

// GetMetrics 获取或创建指标实例
func GetMetrics() *Metrics {
	once.Do(func() {
		instance = newMetrics()
	})
	return instance
}

func newMetrics() *Metrics {
	m := &Metrics{
		httpRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		httpRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"method", "path"},
		),
		grpcRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_requests_total",
				Help: "Total number of gRPC requests",
			},
			[]string{"service", "method", "status"},
		),
		grpcRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "grpc_request_duration_seconds",
				Help:    "gRPC request duration in seconds",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"service", "method"},
		),
		grpcClientsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_clients_requests_total",
				Help: "Total number of gRPC client requests",
			},
			[]string{"client", "method", "status"},
		),
		grpcClientFailuresTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_clients_failures_total",
				Help: "Total number of gRPC client failures",
			},
			[]string{"client", "method", "error_type"},
		),
		grpcClientCircuitState: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "grpc_clients_circuit_state",
				Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
			},
			[]string{"client"},
		),
	}

	return m
}

// RecordHTTPRequest 记录 HTTP 请求
func (m *Metrics) RecordHTTPRequest(method, path string, status int, duration time.Duration) {
	m.httpRequestsTotal.WithLabelValues(method, path, strconv.Itoa(status)).Inc()
	m.httpRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
}

// IncHTTPRequestsInFlight 增加正在处理的请求数
func (m *Metrics) IncHTTPRequestsInFlight() {
	atomic.AddInt64(&m.httpRequestsInFlight, 1)
}

// DecHTTPRequestsInFlight 减少正在处理的请求数
func (m *Metrics) DecHTTPRequestsInFlight() {
	atomic.AddInt64(&m.httpRequestsInFlight, -1)
}

// GetHTTPRequestsInFlight 获取正在处理的请求数
func (m *Metrics) GetHTTPRequestsInFlight() int64 {
	return atomic.LoadInt64(&m.httpRequestsInFlight)
}

// RecordGRPCRequest 记录 gRPC 请求
func (m *Metrics) RecordGRPCRequest(service, method, status string, duration time.Duration) {
	m.grpcRequestsTotal.WithLabelValues(service, method, status).Inc()
	m.grpcRequestDuration.WithLabelValues(service, method).Observe(duration.Seconds())
}

// RecordGRPCClientRequest 记录 gRPC 客户端请求
func (m *Metrics) RecordGRPCClientRequest(client, method, status string) {
	m.grpcClientsTotal.WithLabelValues(client, method, status).Inc()
}

// RecordGRPCClientFailure 记录 gRPC 客户端失败
func (m *Metrics) RecordGRPCClientFailure(client, method, errorType string) {
	m.grpcClientFailuresTotal.WithLabelValues(client, method, errorType).Inc()
}

// RecordCircuitState 记录熔断器状态
func (m *Metrics) RecordCircuitState(client string, state int) {
	m.grpcClientCircuitState.WithLabelValues(client).Set(float64(state))
}
