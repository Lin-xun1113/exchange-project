package metrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Order holds order lifecycle and workflow metrics.
type Order struct {
	created    *prometheus.CounterVec
	final      *prometheus.CounterVec
	outboxStep *prometheus.HistogramVec
	outboxDue  prometheus.Gauge
}

func NewOrder(reg prometheus.Registerer) *Order {
	m := &Order{
		created: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "orders_created_total",
			Help: "Orders created by symbol and side.",
		}, []string{"symbol", "side"}),
		final: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "orders_final_total",
			Help: "Orders entering a terminal state.",
		}, []string{"status"}),
		outboxStep: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "outbox_step_duration_seconds",
			Help: "Outbox attempt duration in seconds by step and result.",
		}, []string{"step", "result"}),
		outboxDue: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbox_due_entries",
			Help: "Pending outbox entries due for processing.",
		}),
	}
	reg.MustRegister(m.created, m.final, m.outboxStep, m.outboxDue)
	return m
}

func (m *Order) OrderCreated(symbol, side string) { m.created.WithLabelValues(symbol, side).Inc() }

func (m *Order) OrderFinal(status string) { m.final.WithLabelValues(status).Inc() }

func (m *Order) OutboxStep(step, result string, d time.Duration) {
	// Settlement keys include a trade ID. Keep only the step name.
	name, _, _ := strings.Cut(step, ":")
	m.outboxStep.WithLabelValues(name, result).Observe(d.Seconds())
}

func (m *Order) OutboxDue() prometheus.Gauge { return m.outboxDue }
