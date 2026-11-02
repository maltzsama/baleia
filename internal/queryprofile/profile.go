// Package queryprofile parses the payload Lens stores for a Trino query
// (#532) into the neutral profile the API renders. It keeps internal/trino out
// of the domain packages, which must reach SQL through internal/queryengine.
package queryprofile

import (
	"encoding/json"

	"github.com/galileostudio/baleia/internal/trino"
)

// Profile is a query's execution profile; a nil field is unknown.
type Profile struct {
	QueuedMs        *int64
	AnalysisMs      *int64
	PlanningMs      *int64
	ExecutionMs     *int64
	CPUMs           *int64
	PeakMemoryBytes *int64
	SpilledBytes    *int64
	InputBytes      *int64
	InputRows       *int64
	OutputBytes     *int64
	OutputRows      *int64
	Inputs          []Input
}

// Input is one input table of the query.
type Input struct {
	Catalog string
	Schema  string
	Table   string
	Bytes   *int64
	Rows    *int64
}

// eventProfile is the payload the query-events listener stores (#406): the
// completed event's statistics, never the raw event.
type eventProfile struct {
	Source          string `json:"source"`
	QueuedMs        int64  `json:"queuedMs"`
	AnalysisMs      int64  `json:"analysisMs"`
	PlanningMs      int64  `json:"planningMs"`
	ExecutionMs     int64  `json:"executionMs"`
	CPUMs           int64  `json:"cpuMs"`
	PeakMemoryBytes int64  `json:"peakMemoryBytes"`
	SpilledBytes    int64  `json:"spilledBytes"`
	InputBytes      int64  `json:"inputBytes"`
	InputRows       int64  `json:"inputRows"`
	OutputBytes     int64  `json:"outputBytes"`
	OutputRows      int64  `json:"outputRows"`
	Inputs          []struct {
		Catalog string `json:"catalog"`
		Schema  string `json:"schema"`
		Table   string `json:"table"`
	} `json:"inputs"`
}

// FromPayload parses the two shapes the snapshot payload has: the listener's
// profile and the poller's QueryInfo. Unknown input is nil, never an error.
func FromPayload(raw []byte) *Profile {
	if len(raw) == 0 {
		return nil
	}
	var head struct {
		Source     string          `json:"source"`
		QueryStats json.RawMessage `json:"queryStats"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return nil
	}
	if head.Source == "query_completed_event" {
		var p eventProfile
		if json.Unmarshal(raw, &p) != nil {
			return nil
		}
		out := &Profile{
			QueuedMs:        ptr(p.QueuedMs),
			AnalysisMs:      ptr(p.AnalysisMs),
			PlanningMs:      ptr(p.PlanningMs),
			ExecutionMs:     ptr(p.ExecutionMs),
			CPUMs:           ptr(p.CPUMs),
			PeakMemoryBytes: ptr(p.PeakMemoryBytes),
			SpilledBytes:    ptr(p.SpilledBytes),
			InputBytes:      ptr(p.InputBytes),
			InputRows:       ptr(p.InputRows),
			OutputBytes:     ptr(p.OutputBytes),
			OutputRows:      ptr(p.OutputRows),
		}
		for _, in := range p.Inputs {
			out.Inputs = append(out.Inputs, Input{Catalog: in.Catalog, Schema: in.Schema, Table: in.Table})
		}
		return out
	}

	var q trino.QueryInfo
	if len(head.QueryStats) == 0 || json.Unmarshal(raw, &q) != nil {
		return nil
	}
	r := q.Resources()
	inputBytes := r.ProcessedBytes
	if inputBytes == nil {
		inputBytes = q.PhysicalInputBytes()
	}
	out := &Profile{
		QueuedMs:        r.QueuedMs,
		PlanningMs:      r.PlanningMs,
		ExecutionMs:     r.ExecutionMs,
		CPUMs:           int64Ptr(q.CPUMs()),
		PeakMemoryBytes: r.PeakMemoryBytes,
		SpilledBytes:    r.SpilledBytes,
		InputBytes:      inputBytes,
		InputRows:       r.ProcessedRows,
		OutputBytes:     r.OutputBytes,
		OutputRows:      r.OutputRows,
	}
	for _, in := range r.Inputs {
		out.Inputs = append(out.Inputs, Input{
			Catalog: in.Catalog, Schema: in.Schema, Table: in.Table, Bytes: in.Bytes, Rows: in.Rows,
		})
	}
	return out
}

func ptr(v int64) *int64 { return &v }

func int64Ptr(v *int) *int64 {
	if v == nil {
		return nil
	}
	n := int64(*v)
	return &n
}
