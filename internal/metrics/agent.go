package metrics

import "github.com/prometheus/client_golang/prometheus"

// Agent counts the capability gate decisions (#60).
type Agent struct {
	gate *prometheus.CounterVec
}

// Agent registers baleia_agent_gate_decisions_total{tool,outcome,reason}.
// outcome is allowed or refused; reason is empty for allowed calls and one of
// the bounded agentpolicy ReasonCode values for refusals.
func (r *Registry) Agent() *Agent {
	a := &Agent{gate: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "agent_gate_decisions_total",
		Help:      "Agent capability gate decisions by tool, outcome and reason.",
	}, []string{"tool", "outcome", "reason"})}
	r.reg.MustRegister(a.gate)
	return a
}

// Decision records one gate decision.
func (a *Agent) Decision(tool string, allowed bool, reason string) {
	if a == nil {
		return
	}
	outcome := "refused"
	if allowed {
		outcome = "allowed"
		reason = ""
	}
	a.gate.WithLabelValues(tool, outcome, reason).Inc()
}
