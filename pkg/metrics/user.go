package metrics

import "github.com/prometheus/client_golang/prometheus"

// User holds user service ledger metrics.
type User struct {
	ledgerOps *prometheus.CounterVec
}

func NewUser(reg prometheus.Registerer) *User {
	m := &User{ledgerOps: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ledger_operations_total",
		Help: "Ledger operations by operation and result.",
	}, []string{"op", "result"})}
	reg.MustRegister(m.ledgerOps)
	return m
}

func (m *User) LedgerOp(op, result string) { m.ledgerOps.WithLabelValues(op, result).Inc() }
