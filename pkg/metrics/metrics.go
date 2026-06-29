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
	httpRequestsTotal     *prometheus.CounterVec
	httpRequestDuration   *prometheus.HistogramVec
	httpRequestsInFlight  int64

	// gRPC client metrics
	grpcRequestsTotal        *prometheus.CounterVec
	grpcRequestDuration      *prometheus.HistogramVec
	grpcClientsTotal         *prometheus.CounterVec
	grpcClientFailuresTotal  *prometheus.CounterVec
	grpcClientCircuitState   *prometheus.GaugeVec

	// gRPC server metrics
	grpcServerRequestsTotal  *prometheus.CounterVec
	grpcServerDuration       *prometheus.HistogramVec
)

// Metrics 指标收集器
type Metrics struct {
	// HTTP metrics
	httpRequestsTotal     *prometheus.CounterVec
	httpRequestDuration   *prometheus.HistogramVec
	httpRequestsInFlight  int64

	// gRPC client metrics
	grpcRequestsTotal        *prometheus.CounterVec
	grpcRequestDuration      *prometheus.HistogramVec
	grpcClientsTotal         *prometheus.CounterVec
	grpcClientFailuresTotal  *prometheus.CounterVec
	grpcClientCircuitState   *prometheus.GaugeVec

	// gRPC server metrics
	grpcServerRequestsTotal  *prometheus.CounterVec
	grpcServerDuration       *prometheus.HistogramVec

	// Order metrics
	orderCreatedTotal    *prometheus.CounterVec
	orderCancelledTotal *prometheus.CounterVec
	orderFillRate       *prometheus.HistogramVec

	// Order book metrics
	orderbookDepthLevels *prometheus.GaugeVec
	orderbookBestBid    *prometheus.GaugeVec
	orderbookBestAsk    *prometheus.GaugeVec

	// Phase 3: Order book orders total gauge (by side for cardinality control)
	orderbookOrdersTotal *prometheus.GaugeVec

	// Phase 3: Order book depth bucket histogram
	orderbookDepthBucket *prometheus.HistogramVec

	// Matching engine metrics
	matchingLatencySeconds *prometheus.HistogramVec

	// Phase 3: Matching match latency histogram
	matchingMatchLatencySeconds *prometheus.HistogramVec

	matchingMatchTotal *prometheus.CounterVec

	// WAL metrics
	matchingWALAppendSeconds *prometheus.HistogramVec
	matchingWALFsyncSeconds *prometheus.HistogramVec
	matchingWALPendingEntries prometheus.Gauge
	matchingWALGroupSize *prometheus.HistogramVec

	// Rate limiting metrics
	rateLimitBlockedTotal   *prometheus.CounterVec
	rateLimitErrorsTotal   *prometheus.CounterVec
	rateLimitRequestsTotal *prometheus.CounterVec

	// Phase 3: Rate limiting metrics with enhanced labels
	rateLimitRemaining *prometheus.GaugeVec

	// Circuit breaker metrics
	circuitBreakerState *prometheus.GaugeVec

	// Saga metrics (placeholder for future Saga implementation)
	sagaStateTransitionsTotal *prometheus.CounterVec
	sagaRetryTotal           *prometheus.CounterVec

	// Phase 3: Outbox metrics
	outboxPendingEntriesTotal *prometheus.GaugeVec
	outboxProcessingDuration *prometheus.HistogramVec

	// Phase 3: Gateway request duration histogram
	gatewayRequestDuration *prometheus.HistogramVec

	// Distributed tracing metrics
	traceExporterExportTotal *prometheus.CounterVec
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
		// HTTP metrics
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

		// gRPC client metrics
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

		// gRPC server metrics
		grpcServerRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_server_requests_total",
				Help: "Total number of gRPC server requests",
			},
			[]string{"service", "method", "code"},
		),
		grpcServerDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "grpc_server_duration_seconds",
				Help:    "gRPC server request duration in seconds",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"service", "method"},
		),

		// Order metrics
		orderCreatedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "order_created_total",
				Help: "Total number of orders created",
			},
			[]string{"side", "symbol"},
		),
		orderCancelledTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "order_cancelled_total",
				Help: "Total number of orders cancelled",
			},
			[]string{"side", "symbol"},
		),
		orderFillRate: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "order_fill_rate",
				Help:    "Order fill rate (filled quantity / original quantity)",
				Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 0.75, 0.9, 0.95, 0.99, 1.0},
			},
			[]string{"side", "symbol"},
		),

		// Order book metrics
		orderbookDepthLevels: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "orderbook_depth_levels",
				Help: "Number of price levels in the order book",
			},
			[]string{"side", "symbol"},
		),
		orderbookBestBid: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "orderbook_best_bid",
				Help: "Best bid price in the order book",
			},
			[]string{"symbol"},
		),
		orderbookBestAsk: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "orderbook_best_ask",
				Help: "Best ask price in the order book",
			},
			[]string{"symbol"},
		),

		// Phase 3: Order book orders total gauge (by side for cardinality control)
		orderbookOrdersTotal: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matching_orderbook_orders_total",
				Help: "Total number of orders in the order book by side",
			},
			[]string{"side", "symbol"},
		),

		// Phase 3: Order book depth bucket histogram
		orderbookDepthBucket: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "matching_orderbook_depth_bucket",
				Help:    "Order book depth (number of levels) distribution",
				Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
			},
			[]string{"side", "symbol"},
		),

		// Matching engine metrics
		matchingLatencySeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "matching_latency_seconds",
				Help:    "Matching engine operation latency in seconds",
				Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .5, 1},
			},
			[]string{"operation", "symbol"},
		),

		// Phase 3: Matching match latency histogram (latency from order submission to match completion)
		matchingMatchLatencySeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "matching_match_latency_seconds",
				Help:    "Match latency in seconds (time from order submission to match completion)",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"symbol", "order_type"},
		),

		matchingMatchTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matching_match_total",
				Help: "Total number of matches produced by the matching engine",
			},
			[]string{"side", "symbol"},
		),

		// WAL metrics
		matchingWALAppendSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "matching_wal_append_seconds",
				Help:    "WAL append latency in seconds",
				Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1},
			},
			[]string{"sync_mode"},
		),
		matchingWALFsyncSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "matching_wal_fsync_seconds",
				Help:    "WAL fsync latency in seconds",
				Buckets: []float64{.00005, .0001, .0005, .001, .005, .01, .025, .05, .1},
			},
			[]string{"status"},
		),
		matchingWALPendingEntries: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "matching_wal_pending_entries",
				Help: "Number of unflushed WAL entries pending sync",
			},
		),
		matchingWALGroupSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "matching_wal_group_size",
				Help:    "Number of entries batched per WAL fsync",
				Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
			},
			[]string{},
		),

		// Rate limiting metrics
		rateLimitBlockedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rate_limit_blocked_total",
				Help: "Total number of requests blocked by rate limiter",
			},
			[]string{"scope", "identity", "policy"},
		),
		rateLimitErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rate_limit_errors_total",
				Help: "Total number of rate limiter errors",
			},
			[]string{"scope"},
		),
		rateLimitRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rate_limit_requests_total",
				Help: "Total number of requests processed by rate limiter",
			},
			[]string{"scope", "policy"},
		),

		// Phase 3: Rate limiting remaining requests gauge
		rateLimitRemaining: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rate_limit_remaining",
				Help: "Remaining requests allowed in the current window",
			},
			[]string{"scope", "identity"},
		),

		// Circuit breaker metrics
		circuitBreakerState: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "circuit_breaker_state",
				Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
			},
			[]string{"name"},
		),

		// Saga metrics (placeholder for future Saga implementation)
		sagaStateTransitionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "saga_state_transitions_total",
				Help: "Total number of saga state transitions (for future Saga implementation)",
			},
			[]string{"from", "to"},
		),
		sagaRetryTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "saga_retry_total",
				Help: "Total number of saga step retries (for future Saga implementation)",
			},
			[]string{"step"},
		),

		// Phase 3: Outbox metrics
		outboxPendingEntriesTotal: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "outbox_pending_entries_total",
				Help: "Total number of pending outbox entries by status",
			},
			[]string{"status"},
		),
		outboxProcessingDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "outbox_processing_duration_seconds",
				Help:    "Outbox entry processing duration in seconds",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"action_type"},
		),

		// Phase 3: Gateway request duration histogram
		gatewayRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_request_duration_seconds",
				Help:    "Gateway HTTP request duration in seconds",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"method", "path", "status"},
		),

		// Distributed tracing metrics
		traceExporterExportTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "trace_exporter_export_total",
				Help: "Total number of trace export operations",
			},
			[]string{"result"},
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

// RecordGRPCRequest 记录 gRPC 客户端请求
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

// RecordGRPCServerRequest 记录 gRPC 服务端请求
func (m *Metrics) RecordGRPCServerRequest(service, method, code string, duration time.Duration) {
	m.grpcServerRequestsTotal.WithLabelValues(service, method, code).Inc()
	m.grpcServerDuration.WithLabelValues(service, method).Observe(duration.Seconds())
}

// RecordOrderCreated 记录订单创建
func (m *Metrics) RecordOrderCreated(side, symbol string) {
	m.orderCreatedTotal.WithLabelValues(side, symbol).Inc()
}

// RecordOrderCancelled 记录订单取消
func (m *Metrics) RecordOrderCancelled(side, symbol string) {
	m.orderCancelledTotal.WithLabelValues(side, symbol).Inc()
}

// RecordOrderFillRate 记录订单成交率
func (m *Metrics) RecordOrderFillRate(side, symbol string, fillRate float64) {
	m.orderFillRate.WithLabelValues(side, symbol).Observe(fillRate)
}

// SetOrderbookDepthLevels 设置订单簿深度
func (m *Metrics) SetOrderbookDepthLevels(side, symbol string, levels float64) {
	m.orderbookDepthLevels.WithLabelValues(side, symbol).Set(levels)
}

// SetOrderbookBestBid 设置最优买价
func (m *Metrics) SetOrderbookBestBid(symbol string, price float64) {
	m.orderbookBestBid.WithLabelValues(symbol).Set(price)
}

// SetOrderbookBestAsk 设置最优卖价
func (m *Metrics) SetOrderbookBestAsk(symbol string, price float64) {
	m.orderbookBestAsk.WithLabelValues(symbol).Set(price)
}

// RecordMatchingLatency 记录撮合延迟
func (m *Metrics) RecordMatchingLatency(operation, symbol string, duration time.Duration) {
	m.matchingLatencySeconds.WithLabelValues(operation, symbol).Observe(duration.Seconds())
}

// RecordMatchingMatch 记录撮合成交次数
func (m *Metrics) RecordMatchingMatch(side, symbol string, count float64) {
	m.matchingMatchTotal.WithLabelValues(side, symbol).Add(count)
}

// RecordWALAppendLatency 记录 WAL 追加延迟
func (m *Metrics) RecordWALAppendLatency(duration time.Duration, syncMode string) {
	m.matchingWALAppendSeconds.WithLabelValues(syncMode).Observe(duration.Seconds())
}

// RecordWALFsyncLatency 记录 WAL fsync 延迟
func (m *Metrics) RecordWALFsyncLatency(duration time.Duration, status string) {
	m.matchingWALFsyncSeconds.WithLabelValues(status).Observe(duration.Seconds())
}

// SetWALPendingEntries 设置待同步 WAL 条目数
func (m *Metrics) SetWALPendingEntries(count int64) {
	m.matchingWALPendingEntries.Set(float64(count))
}

// RecordWALGroupSize 记录 Group Commit 批次大小
func (m *Metrics) RecordWALGroupSize(size int64) {
	m.matchingWALGroupSize.WithLabelValues().Observe(float64(size))
}

// RecordRateLimitBlocked 记录限流拦截
func (m *Metrics) RecordRateLimitBlocked(scope, identity, policy string) {
	m.rateLimitBlockedTotal.WithLabelValues(scope, identity, policy).Inc()
}

// IncRateLimitBlocked 增加限流拦截计数 (兼容方法)
func (m *Metrics) IncRateLimitBlocked(scope, identity, policy string) {
	m.rateLimitBlockedTotal.WithLabelValues(scope, identity, policy).Inc()
}

// IncRateLimitErrors 增加限流错误计数
func (m *Metrics) IncRateLimitErrors() {
	m.rateLimitErrorsTotal.WithLabelValues("all").Inc()
}

// IncRateLimitErrorsWithScope 增加限流错误计数 (带 scope)
func (m *Metrics) IncRateLimitErrorsWithScope(scope string) {
	m.rateLimitErrorsTotal.WithLabelValues(scope).Inc()
}

// RecordRateLimitRequest 记录限流请求
func (m *Metrics) RecordRateLimitRequest(scope, policy string) {
	m.rateLimitRequestsTotal.WithLabelValues(scope, policy).Inc()
}

// RecordCircuitBreakerState 记录熔断器状态
func (m *Metrics) RecordCircuitBreakerState(name string, state float64) {
	m.circuitBreakerState.WithLabelValues(name).Set(state)
}

// RecordSagaStateTransition 记录 Saga 状态转移
func (m *Metrics) RecordSagaStateTransition(fromState, toState string) {
	m.sagaStateTransitionsTotal.WithLabelValues(fromState, toState).Inc()
}

// RecordSagaRetry 记录 Saga 重试
func (m *Metrics) RecordSagaRetry(step string) {
	m.sagaRetryTotal.WithLabelValues(step).Inc()
}

// RecordTraceExporterExport 记录 Trace 导出结果
func (m *Metrics) RecordTraceExporterExport(result string) {
	m.traceExporterExportTotal.WithLabelValues(result).Inc()
}

// Phase 3: Order book orders total gauge

// SetOrderbookOrdersTotal sets the total number of orders in the order book
func (m *Metrics) SetOrderbookOrdersTotal(side, symbol string, count float64) {
	m.orderbookOrdersTotal.WithLabelValues(side, symbol).Set(count)
}

// ObserveOrderbookDepthBucket observes the order book depth into a histogram
func (m *Metrics) ObserveOrderbookDepthBucket(side, symbol string, depth float64) {
	m.orderbookDepthBucket.WithLabelValues(side, symbol).Observe(depth)
}

// Phase 3: Matching match latency histogram

// RecordMatchingMatchLatency records the latency from order submission to match completion
func (m *Metrics) RecordMatchingMatchLatency(symbol, orderType string, duration time.Duration) {
	m.matchingMatchLatencySeconds.WithLabelValues(symbol, orderType).Observe(duration.Seconds())
}

// Phase 3: Rate limiting remaining

// SetRateLimitRemaining sets the remaining requests in the current window
func (m *Metrics) SetRateLimitRemaining(scope, identity string, remaining float64) {
	m.rateLimitRemaining.WithLabelValues(scope, identity).Set(remaining)
}

// Phase 3: Outbox metrics

// SetOutboxPendingEntries sets the count of pending outbox entries by status
func (m *Metrics) SetOutboxPendingEntries(status string, count float64) {
	m.outboxPendingEntriesTotal.WithLabelValues(status).Set(count)
}

// RecordOutboxProcessingDuration records the outbox entry processing duration
func (m *Metrics) RecordOutboxProcessingDuration(actionType string, duration time.Duration) {
	m.outboxProcessingDuration.WithLabelValues(actionType).Observe(duration.Seconds())
}

// Phase 3: Gateway request duration

// RecordGatewayRequest records gateway request duration
func (m *Metrics) RecordGatewayRequest(method, path string, status int, duration time.Duration) {
	m.gatewayRequestDuration.WithLabelValues(method, path, strconv.Itoa(status)).Observe(duration.Seconds())
}
