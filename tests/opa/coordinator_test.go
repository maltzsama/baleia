//go:build opa

package opa

import (
	"bufio"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// dataRequests sums OPA's served /v1/data requests from its Prometheus
// metrics (diagnostic port).
func dataRequests(t testing.TB, addr string) int {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	total := 0
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "http_request_duration_seconds_count{") || !strings.Contains(line, `handler="v1/data"`) {
			continue
		}
		fields := strings.Fields(line)
		n, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatal(err)
		}
		total += int(n)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return total
}

// Q6. Coordinator only. The worker runs with the same access controls and its
// own OPA. Splits run only on the worker (node-scheduler.include-coordinator
// is false), and its OPA must serve no decision, while the coordinator's
// serves all of them.
func TestCoordinatorOnly_WorkerNeverAsksOPA(t *testing.T) {
	c := connect(t, "svc/trino-opa")
	table := seedOrders(t, c, "orders_workers")
	publish(t, ordersPolicies("q6", "orders_workers"), "trino-opa", "trino-opa-worker")

	nodes := c.query(t, admin, "SELECT count(*) FROM system.runtime.nodes WHERE state = 'active'").strings()
	if len(nodes) != 1 || nodes[0] != "2" {
		t.Fatalf("active nodes = %q, want 2 (coordinator and worker)", nodes)
	}
	worker := forward(t, "pod/"+podName(t, "trino-opa-worker"), 8282)
	coordinator := forward(t, "pod/"+podName(t, "trino-opa"), 8282)
	workerBefore, coordinatorBefore := dataRequests(t, worker), dataRequests(t, coordinator)

	u := user("u_ana")
	for _, sql := range []string{
		"SELECT id, email FROM " + table,
		"SELECT region, count(*) FROM " + table + " GROUP BY region",
		"SELECT a.id FROM " + table + " a JOIN " + table + " b ON a.id = b.id",
		"SELECT count(*) FROM tpch.tiny.lineitem",
	} {
		if res, err := c.run(t.Context(), u, sql); err != nil {
			t.Fatal(err)
		} else if res.err != nil {
			t.Logf("%s: %v", sql, res.err)
		}
	}
	// A row-filtered write (INSERT is checked against the filter).
	c.query(t, admin, "CREATE TABLE IF NOT EXISTS iceberg.spike.orders_workers_copy (id integer)")
	c.query(t, u, "INSERT INTO iceberg.spike.orders_workers_copy SELECT id FROM "+table)

	time.Sleep(2 * time.Second)
	workerCalls := dataRequests(t, worker) - workerBefore
	coordinatorCalls := dataRequests(t, coordinator) - coordinatorBefore
	t.Logf("OPA decisions during the workload: coordinator %d, worker %d", coordinatorCalls, workerCalls)
	if workerCalls != 0 {
		t.Errorf("the worker's OPA served %d decisions, want 0", workerCalls)
	}
	if coordinatorCalls == 0 {
		t.Error("the coordinator's OPA served no decision: the workload did not exercise it")
	}
	tasks := c.query(t, admin, fmt.Sprintf("SELECT count(*) FROM system.runtime.tasks t JOIN system.runtime.nodes n ON t.node_id = n.node_id WHERE n.coordinator = false AND t.query_id IN (SELECT query_id FROM system.runtime.queries WHERE \"user\" = '%s')", "u_ana")).strings()
	t.Logf("tasks of u_ana's queries on the worker: %v", tasks)
	if len(tasks) != 1 || tasks[0] == "0" {
		t.Errorf("no task ran on the worker: %q", tasks)
	}
}
