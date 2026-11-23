//go:build opa

package opa

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// decision is one OPA decision log entry (decision_logs.console: true).
type decision struct {
	Timestamp time.Time      `json:"timestamp"`
	Path      string         `json:"path"`
	Input     map[string]any `json:"input"`
	Result    any            `json:"result"`
	Bundles   map[string]struct {
		Revision string `json:"revision"`
	} `json:"bundles"`
	Metrics struct {
		HandlerNs int64 `json:"timer_server_handler_ns"`
		EvalNs    int64 `json:"timer_rego_query_eval_ns"`
	} `json:"metrics"`
}

func (d decision) operation() string {
	action, _ := d.Input["action"].(map[string]any)
	op, _ := action["operation"].(string)
	return op
}

func (d decision) queryID() string {
	ctx, _ := d.Input["context"].(map[string]any)
	id, _ := ctx["queryId"].(string)
	return id
}

func (d decision) user() string {
	ctx, _ := d.Input["context"].(map[string]any)
	identity, _ := ctx["identity"].(map[string]any)
	user, _ := identity["user"].(string)
	return user
}

// decisionsSince returns the decisions the deployment's OPA logged since.
func decisionsSince(t testing.TB, deployment string, since time.Time) []decision {
	t.Helper()
	out := mustKubectl(t, "logs", "pod/"+podName(t, deployment), "-c", "opa",
		"--since-time="+since.Add(-time.Second).UTC().Format(time.RFC3339))
	var decisions []decision
	for line := range strings.Lines(out) {
		if !strings.Contains(line, `"Decision Log"`) {
			continue
		}
		var d decision
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatalf("decision log line %q: %v", line, err)
		}
		if d.Timestamp.Before(since) {
			continue // --since-time has a one-second granularity
		}
		decisions = append(decisions, d)
	}
	return decisions
}
