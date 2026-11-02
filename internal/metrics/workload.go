package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Workload records the per-engine admission queues (#399, ADR 0014). The
// labels are the engine id and the workload class — never a user, a workspace
// or SQL.
type Workload struct {
	depth      *prometheus.GaugeVec
	running    *prometheus.GaugeVec
	wait       *prometheus.HistogramVec
	admissions *prometheus.CounterVec
	timeouts   *prometheus.CounterVec
}

// Workload registers the baleia_execution_queue_* metrics.
func (r *Registry) Workload() *Workload {
	w := &Workload{
		depth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "execution_queue_depth", Help: "Runs waiting in an engine's queue, by class.",
		}, []string{"engine", "class"}),
		running: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "execution_queue_running", Help: "Runs holding a slot on an engine, by class.",
		}, []string{"engine", "class"}),
		wait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Name: "execution_queue_wait_seconds", Help: "How long an admitted run waited in the queue.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300, 900, 1800, 3600, 21600, 86400},
		}, []string{"engine", "class"}),
		admissions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "execution_admissions_total", Help: "Runs admitted from an engine's queue, by class.",
		}, []string{"engine", "class"}),
		timeouts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "execution_queue_timeouts_total", Help: "Runs that timed out waiting in an engine's queue, by class.",
		}, []string{"engine", "class"}),
	}
	r.reg.MustRegister(w.depth, w.running, w.wait, w.admissions, w.timeouts)
	return w
}

// State sets the depth and running gauges of one (engine, class) queue.
func (w *Workload) State(engine, class string, running, queued int) {
	if w == nil {
		return
	}
	w.running.WithLabelValues(engine, class).Set(float64(running))
	w.depth.WithLabelValues(engine, class).Set(float64(queued))
}

// Admitted records one admission and how long the run waited.
func (w *Workload) Admitted(engine, class string, wait time.Duration) {
	if w == nil {
		return
	}
	w.admissions.WithLabelValues(engine, class).Inc()
	w.wait.WithLabelValues(engine, class).Observe(wait.Seconds())
}

// Timeout records one run that timed out waiting.
func (w *Workload) Timeout(engine, class string) {
	if w == nil {
		return
	}
	w.timeouts.WithLabelValues(engine, class).Inc()
}
