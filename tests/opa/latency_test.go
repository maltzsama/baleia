//go:build opa

package opa

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// Q5. Latency. The same statements run on the coordinator without OPA, with
// OPA through the non-batched endpoints (both single-node) and with the
// batched endpoints (coordinator and worker). Per query: client-side wall
// time, and from OPA's decision log how many decisions it served and how long
// its handler took. Decision logging to the console is on in every OPA
// variant, so the OPA numbers are an upper bound. A measurement, not a gate.
func TestLatency_PerQueryOverhead(t *testing.T) {
	const runs = 30
	setup := connect(t, "svc/trino-baseline")
	table := seedOrders(t, setup, "orders_latency")
	publish(t, ordersPolicies("q5", "orders_latency"), "trino-opa", "trino-opa-single")

	workloads := []struct {
		name string
		sql  string
	}{
		{"unpolicied tpch", "SELECT count(*) FROM tpch.tiny.nation"},
		{"policied table, filter and mask", "SELECT id, email FROM " + table + " WHERE id > 0"},
		{"policied self-join", "SELECT a.id, b.email FROM " + table + " a JOIN " + table + " b ON a.id = b.id"},
	}
	var report []string
	for _, variant := range []string{"trino-baseline", "trino-opa-single", "trino-opa"} {
		c := connect(t, "svc/"+variant)
		for _, w := range workloads {
			for range 5 { // warm-up: plans, metadata and connections
				c.query(t, user("u_ana"), w.sql)
			}
			start := time.Now()
			var elapsed []time.Duration
			ids := map[string]bool{}
			for range runs {
				res := c.query(t, user("u_ana"), w.sql)
				elapsed = append(elapsed, res.elapsed)
				ids[res.queryID] = true
			}
			line := fmt.Sprintf("%-17s %-33s p50 %6.1fms p95 %6.1fms", variant, w.name,
				ms(percentile(elapsed, 50)), ms(percentile(elapsed, 95)))
			if variant != "trino-baseline" {
				calls, handler := 0, time.Duration(0)
				for _, d := range decisionsSince(t, variant, start) {
					// ExecuteQuery carries no query id; count one per query.
					if ids[d.queryID()] || (d.operation() == "ExecuteQuery" && d.user() == "u_ana") {
						calls++
						handler += time.Duration(d.Metrics.HandlerNs)
					}
				}
				line += fmt.Sprintf("  OPA: %4.1f decisions/query, %5.2fms handler/query",
					float64(calls)/runs, ms(handler)/runs)
			}
			report = append(report, line)
		}
	}
	t.Logf("latency over %d runs per row:\n%s", runs, strings.Join(report, "\n"))
}

func percentile(values []time.Duration, p int) time.Duration {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[(len(sorted)-1)*p/100]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
