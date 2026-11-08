package metrics

import "github.com/prometheus/client_golang/prometheus"

// Monitoring carries the engine cluster gauges (#828), labelled by engine kind
// (a bounded label; engine names are not allowed).
type Monitoring struct {
	workers *prometheus.GaugeVec
	running *prometheus.GaugeVec
	queued  *prometheus.GaugeVec
	blocked *prometheus.GaugeVec
	memory  *prometheus.GaugeVec
}

// Monitoring registers the baleia_cluster_* metrics.
func (r *Registry) Monitoring() *Monitoring {
	m := &Monitoring{
		workers: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "cluster_active_workers", Help: "Active workers in the engine's latest cluster snapshot.",
		}, []string{"engine"}),
		running: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "cluster_running_queries", Help: "Running queries in the engine's latest cluster snapshot.",
		}, []string{"engine"}),
		queued: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "cluster_queued_queries", Help: "Queued queries in the engine's latest cluster snapshot.",
		}, []string{"engine"}),
		blocked: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "cluster_blocked_queries", Help: "Blocked queries in the engine's latest cluster snapshot.",
		}, []string{"engine"}),
		memory: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "cluster_memory_used_ratio", Help: "Share of the engine's memory in use (0..1).",
		}, []string{"engine"}),
	}
	r.reg.MustRegister(m.workers, m.running, m.queued, m.blocked, m.memory)
	return m
}

// Cluster records the latest snapshot; memory is nil when the engine does not
// report it, and the previous value is left untouched then.
func (m *Monitoring) Cluster(engine string, workers, running, queued, blocked int, memory *float64) {
	if m == nil {
		return
	}
	m.workers.WithLabelValues(engine).Set(float64(workers))
	m.running.WithLabelValues(engine).Set(float64(running))
	m.queued.WithLabelValues(engine).Set(float64(queued))
	m.blocked.WithLabelValues(engine).Set(float64(blocked))
	if memory != nil {
		m.memory.WithLabelValues(engine).Set(*memory)
	}
}
