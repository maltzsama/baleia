package trino

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// QueryInfo is the subset of Trino's /v1/query (BasicQueryInfo) and
// /v1/query/{id} payloads that Lens records. Field names follow Trino 482:
// user, catalog and schema live under session; timings and sizes are strings
// with units ("436.86ms", "469B"); errorCode is an object.
type QueryInfo struct {
	QueryID   string `json:"queryId"`
	State     string `json:"state"`
	Query     string `json:"query"`
	QueryType string `json:"queryType"`
	Session   struct {
		User    string  `json:"user"`
		Catalog *string `json:"catalog"`
		Schema  *string `json:"schema"`
	} `json:"session"`
	QueryStats struct {
		CreateTime            string `json:"createTime"`
		EndTime               string `json:"endTime"`
		ElapsedTime           string `json:"elapsedTime"`
		TotalCPUTime          string `json:"totalCpuTime"`
		PhysicalInputDataSize string `json:"physicalInputDataSize"`
		// The rest comes only with /v1/query/{id} (GetQuery, #407).
		QueuedTime                string `json:"queuedTime"`
		PlanningTime              string `json:"planningTime"`
		PeakUserMemoryReservation string `json:"peakUserMemoryReservation"`
		ProcessedInputPositions   *int64 `json:"processedInputPositions"`
		ProcessedInputDataSize    string `json:"processedInputDataSize"`
		OutputPositions           *int64 `json:"outputPositions"`
		OutputDataSize            string `json:"outputDataSize"`
		FullyBlocked              bool   `json:"fullyBlocked"`
	} `json:"queryStats"`
	ErrorType *string `json:"errorType"`
	ErrorCode *struct {
		Code int    `json:"code"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"errorCode"`
	// FailureInfo is only present on /v1/query/{id}.
	FailureInfo *struct {
		Message string `json:"message"`
	} `json:"failureInfo"`
}

// User, Catalog and Schema return the session values, empty when unset.
func (q QueryInfo) User() string { return q.Session.User }

func (q QueryInfo) Catalog() string { return deref(q.Session.Catalog) }

func (q QueryInfo) Schema() string { return deref(q.Session.Schema) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CreatedAt parses queryStats.createTime; ok is false when absent or malformed.
func (q QueryInfo) CreatedAt() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, q.QueryStats.CreateTime)
	return t, err == nil
}

// ElapsedMs and CPUMs return nil when the value is absent or unparsable.
func (q QueryInfo) ElapsedMs() *int { return durationMs(q.QueryStats.ElapsedTime) }

func (q QueryInfo) CPUMs() *int { return durationMs(q.QueryStats.TotalCPUTime) }

// PhysicalInputBytes returns nil when absent or unparsable.
func (q QueryInfo) PhysicalInputBytes() *int64 {
	b, ok := ParseDataSize(q.QueryStats.PhysicalInputDataSize)
	if !ok {
		return nil
	}
	return &b
}

// Resources is what the query used, from the statistics GetQuery returns;
// a field is nil when Trino left it out.
func (q QueryInfo) Resources() Resources {
	s := q.QueryStats
	return Resources{
		PeakMemoryBytes: dataSize(s.PeakUserMemoryReservation),
		ProcessedRows:   s.ProcessedInputPositions,
		ProcessedBytes:  dataSize(s.ProcessedInputDataSize),
		OutputRows:      s.OutputPositions,
		OutputBytes:     dataSize(s.OutputDataSize),
		QueuedMs:        durationMs64(s.QueuedTime),
		PlanningMs:      durationMs64(s.PlanningTime),
	}
}

// Resources is a query's resource usage; nil fields are unknown.
type Resources struct {
	PeakMemoryBytes *int64
	ProcessedRows   *int64
	ProcessedBytes  *int64
	OutputRows      *int64
	OutputBytes     *int64
	QueuedMs        *int64
	PlanningMs      *int64
}

func dataSize(s string) *int64 {
	b, ok := ParseDataSize(s)
	if !ok {
		return nil
	}
	return &b
}

func durationMs64(s string) *int64 {
	ms := durationMs(s)
	if ms == nil {
		return nil
	}
	v := int64(*ms)
	return &v
}

// FailureMessage is the failure text when Trino returned it, else the error
// code name (the list endpoint carries no message). Empty for healthy queries.
func (q QueryInfo) FailureMessage() string {
	if q.FailureInfo != nil && q.FailureInfo.Message != "" {
		return q.FailureInfo.Message
	}
	if q.ErrorCode != nil {
		return q.ErrorCode.Name
	}
	return ""
}

func durationMs(s string) *int {
	ms, ok := ParseDuration(s)
	if !ok {
		return nil
	}
	v := int(math.Round(float64(ms) / float64(time.Millisecond)))
	return &v
}

var durationUnits = map[string]time.Duration{
	"ns": time.Nanosecond, "us": time.Microsecond, "ms": time.Millisecond,
	"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour,
}

// ParseDuration parses Trino's duration strings ("18.00ms", "1.50s", "2.00m").
func ParseDuration(s string) (time.Duration, bool) {
	num, unit := splitUnit(s)
	u, ok := durationUnits[unit]
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(v * float64(u)), true
}

var dataSizeUnits = map[string]float64{
	"B": 1, "kB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40, "PB": 1 << 50,
}

// ParseDataSize parses Trino's data size strings ("469B", "1.50kB", "10MB") into
// bytes (1024-based, as Trino defines them).
func ParseDataSize(s string) (int64, bool) {
	num, unit := splitUnit(s)
	u, ok := dataSizeUnits[unit]
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	return int64(math.Round(v * u)), true
}

func splitUnit(s string) (num, unit string) {
	s = strings.TrimSpace(s)
	i := len(s)
	for i > 0 && (s[i-1] < '0' || s[i-1] > '9') && s[i-1] != '.' {
		i--
	}
	return s[:i], s[i:]
}

// ClusterStats summarises load on the cluster. Memory totals are not exposed
// by Trino's REST API and stay nil.
type ClusterStats struct {
	RunningQueries   int    `json:"runningQueries"`
	QueuedQueries    int    `json:"queuedQueries"`
	BlockedQueries   int    `json:"blockedQueries"`
	ActiveWorkers    int    `json:"activeWorkers"`
	TotalMemoryBytes *int64 `json:"totalMemoryBytes"`
	FreeMemoryBytes  *int64 `json:"freeMemoryBytes"`
}

// ClusterNodesSQL counts the active nodes. Trino 482 has no /v1/cluster or
// /v1/node REST endpoint, so the count comes from system.runtime.nodes. The
// Lens query poller skips this statement so it does not record itself.
const ClusterNodesSQL = "SELECT count(*) FROM system.runtime.nodes WHERE state = 'active'"

// GetClusterStats derives query counts from /v1/query and the worker count
// from system.runtime.nodes.
func (c *Client) GetClusterStats(ctx context.Context) (*ClusterStats, error) {
	queries, err := c.ListQueries(ctx)
	if err != nil {
		return nil, err
	}
	cs := &ClusterStats{}
	for _, q := range queries {
		switch q.State {
		case "QUEUED", "WAITING_FOR_RESOURCES":
			cs.QueuedQueries++
		case "PLANNING", "STARTING", "RUNNING", "FINISHING":
			cs.RunningQueries++
			if q.State == "RUNNING" && q.QueryStats.FullyBlocked {
				cs.BlockedQueries++
			}
		}
	}

	res, err := c.Execute(ctx, ClusterNodesSQL, "", "")
	if err != nil {
		return nil, fmt.Errorf("trino count nodes: %w", err)
	}
	if len(res.Data) == 1 && len(res.Data[0]) == 1 {
		n, perr := strconv.Atoi(fmt.Sprint(res.Data[0][0]))
		if perr != nil {
			return nil, fmt.Errorf("trino count nodes: unexpected value %v", res.Data[0][0])
		}
		cs.ActiveWorkers = n
	}
	return cs, nil
}
