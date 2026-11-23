package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Gateway holds the gateway's HTTP and downstream metrics.
type Gateway struct {
	requests     *prometheus.CounterVec
	duration     *prometheus.HistogramVec
	inFlight     prometheus.Gauge
	rateLimited  *prometheus.CounterVec
	limiterError *prometheus.CounterVec
	breakerState *prometheus.GaugeVec
}

func NewGateway(reg prometheus.Registerer) *Gateway {
	m := &Gateway{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Completed HTTP requests by matched route and response status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "HTTP request duration in seconds by matched route.",
		}, []string{"method", "route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "HTTP requests currently being handled.",
		}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rate_limit_rejected_total",
			Help: "Requests rejected by a rate limit policy.",
		}, []string{"policy"}),
		limiterError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rate_limit_errors_total",
			Help: "Rate limit checks that failed.",
		}, []string{"policy"}),
		breakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "grpc_client_breaker_state",
			Help: "Downstream gRPC circuit breaker state (0 closed, 1 half-open, 2 open).",
		}, []string{"target"}),
	}
	reg.MustRegister(m.requests, m.duration, m.inFlight, m.rateLimited, m.limiterError, m.breakerState)
	return m
}

// ObserveHTTP records an HTTP request using the route template.
func (m *Gateway) ObserveHTTP(method, route string, status int, d time.Duration) {
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(method, route).Observe(d.Seconds())
}

func (m *Gateway) InFlight() prometheus.Gauge { return m.inFlight }

func (m *Gateway) RateLimited(policy string) { m.rateLimited.WithLabelValues(policy).Inc() }

func (m *Gateway) RateLimiterError(policy string) { m.limiterError.WithLabelValues(policy).Inc() }

func (m *Gateway) BreakerState(target string, state int) {
	m.breakerState.WithLabelValues(target).Set(float64(state))
}
