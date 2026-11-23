package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Matching holds matching engine and durability metrics.
type Matching struct {
	command       *prometheus.HistogramVec
	trades        *prometheus.CounterVec
	walSync       prometheus.Histogram
	batchSize     prometheus.Histogram
	snapshot      *prometheus.HistogramVec
	restingOrders *prometheus.GaugeVec
}

func NewMatching(reg prometheus.Registerer) *Matching {
	m := &Matching{
		command: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "matching_command_duration_seconds",
			Help: "Matching command duration in seconds.",
		}, []string{"symbol", "op"}),
		trades: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "matching_trades_total",
			Help: "Trades matched by symbol.",
		}, []string{"symbol"}),
		walSync: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "matching_wal_sync_duration_seconds",
			Help: "WAL sync duration in seconds.",
		}),
		batchSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "matching_batch_size",
			Help:    "Commands processed per matching batch.",
			Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128, 256},
		}),
		snapshot: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "matching_snapshot_duration_seconds",
			Help: "Snapshot write duration in seconds.",
		}, []string{"symbol", "result"}),
		restingOrders: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "matching_resting_orders",
			Help: "Resting orders in the book by symbol.",
		}, []string{"symbol"}),
	}
	reg.MustRegister(m.command, m.trades, m.walSync, m.batchSize, m.snapshot, m.restingOrders)
	return m
}

func (m *Matching) Command(symbol, op string, d time.Duration) {
	m.command.WithLabelValues(symbol, op).Observe(d.Seconds())
}

func (m *Matching) Trades(symbol string, n int) { m.trades.WithLabelValues(symbol).Add(float64(n)) }

func (m *Matching) WALSync(d time.Duration) { m.walSync.Observe(d.Seconds()) }

func (m *Matching) BatchSize(n int) { m.batchSize.Observe(float64(n)) }

func (m *Matching) Snapshot(symbol, result string, d time.Duration) {
	m.snapshot.WithLabelValues(symbol, result).Observe(d.Seconds())
}

func (m *Matching) RestingOrders(symbol string) prometheus.Gauge {
	return m.restingOrders.WithLabelValues(symbol)
}
