// Package agentpolicy is the single enforcement gate for agent tool calls
// (#60). It decides which tools, catalogs, engines and limits an agent has,
// clamps the stored policy to the server ceilings, and returns a typed refusal
// that the MCP server (#58), the UI and the audit trail (#61) all share.
package agentpolicy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Tool names are the v1 MCP registry shared with #58 and the policy UI (#120).
const (
	ToolListWorkspaces  = "list_workspaces"
	ToolListCatalogs    = "list_catalogs"
	ToolListSchemas     = "list_schemas"
	ToolListTables      = "list_tables"
	ToolDescribeTable   = "describe_table"
	ToolSearchMetadata  = "search_metadata"
	ToolListMetrics     = "list_metrics"
	ToolRenderMetric    = "render_metric"
	ToolListModels      = "list_models"
	ToolDescribeModel   = "describe_model"
	ToolQuerySemantic   = "query_semantic"
	ToolRunQuery        = "run_query"
	ToolGetExecution    = "get_execution"
	ToolGetResults      = "get_results"
	ToolCancelExecution = "cancel_execution"
)

// AllTools is the whole v1 registry, sorted. The default policy allows all of
// them; an explicit empty `tools` array denies every tool.
var AllTools = []string{
	ToolListWorkspaces,
	ToolListCatalogs,
	ToolListSchemas,
	ToolListTables,
	ToolDescribeTable,
	ToolSearchMetadata,
	ToolListMetrics,
	ToolRenderMetric,
	ToolListModels,
	ToolDescribeModel,
	ToolQuerySemantic,
	ToolRunQuery,
	ToolGetExecution,
	ToolGetResults,
	ToolCancelExecution,
}

// IsTool reports whether name is in the v1 registry.
func IsTool(name string) bool {
	for _, t := range AllTools {
		if t == name {
			return true
		}
	}
	return false
}

// Limits are the numeric caps of a policy. Zero means "use the default".
type Limits struct {
	MaxRowsReturned        int `json:"max_rows_returned,omitempty"`
	MaxResultBytes         int `json:"max_result_bytes,omitempty"`
	MaxQueryRuntimeSeconds int `json:"max_query_runtime_seconds,omitempty"`
	MaxConcurrentQueries   int `json:"max_concurrent_queries,omitempty"`
	MaxQueriesPerHour      int `json:"max_queries_per_hour,omitempty"`
	MaxToolCallsPerMinute  int `json:"max_tool_calls_per_minute,omitempty"`
}

// Policy is the stored document. A nil Tools/Catalogs/Engines means "use the
// defaults"; an explicit empty list means "none".
type Policy struct {
	Version  int      `json:"version,omitempty"`
	Tools    []string `json:"tools"`
	Catalogs []string `json:"catalogs,omitempty"` // nil = every granted catalog
	Engines  []string `json:"engines,omitempty"`  // nil = any engine hosting them
	Limits
}

// Ceilings are the server-side maxima (BALEIA_AGENT_MAX_*). A policy above a
// ceiling is rejected on save and clamped again at evaluation, so lowering a
// ceiling takes effect without editing policies.
type Ceilings struct {
	MaxRowsReturned        int
	MaxResultBytes         int
	MaxQueryRuntimeSeconds int
	MaxConcurrentQueries   int
	MaxQueriesPerHour      int
	MaxToolCallsPerMinute  int
}

// DefaultCeilings are the hard maxima from #60.
func DefaultCeilings() Ceilings {
	return Ceilings{
		MaxRowsReturned:        5000,
		MaxResultBytes:         1 << 20, // 1 MiB
		MaxQueryRuntimeSeconds: 900,
		MaxConcurrentQueries:   10,
		MaxQueriesPerHour:      2000,
		MaxToolCallsPerMinute:  600,
	}
}

// Defaults are the policy values used when a field is zero.
func Defaults() Limits {
	return Limits{
		MaxRowsReturned:        500,
		MaxResultBytes:         256 << 10, // 256 KiB
		MaxQueryRuntimeSeconds: 120,
		MaxConcurrentQueries:   2,
		MaxQueriesPerHour:      120,
		MaxToolCallsPerMinute:  60,
	}
}

// DefaultPolicy is the policy stored as `{}`.
func DefaultPolicy() Policy {
	return Policy{Version: 1, Tools: append([]string(nil), AllTools...), Limits: Defaults()}
}

// Effective is the evaluated policy handed to a tool. Tools is a set; an empty
// Catalogs/Engines set means "any" (unrestricted), matching a nil list.
type Effective struct {
	Tools                  map[string]bool
	Catalogs               map[string]bool
	Engines                map[string]bool
	MaxRowsReturned        int
	MaxResultBytes         int
	MaxQueryRuntimeSeconds int
	MaxConcurrentQueries   int
	MaxQueriesPerHour      int
	MaxToolCallsPerMinute  int
}

// Effective applies the defaults, then clamps every limit to the ceiling.
func (p Policy) Effective(c Ceilings) Effective {
	tools := p.Tools
	if tools == nil {
		tools = AllTools
	}
	e := Effective{
		Tools:                  make(map[string]bool, len(tools)),
		Catalogs:               uuidSet(p.Catalogs),
		Engines:                uuidSet(p.Engines),
		MaxRowsReturned:        clamp(p.MaxRowsReturned, Defaults().MaxRowsReturned, c.MaxRowsReturned),
		MaxResultBytes:         clamp(p.MaxResultBytes, Defaults().MaxResultBytes, c.MaxResultBytes),
		MaxQueryRuntimeSeconds: clamp(p.MaxQueryRuntimeSeconds, Defaults().MaxQueryRuntimeSeconds, c.MaxQueryRuntimeSeconds),
		MaxConcurrentQueries:   clamp(p.MaxConcurrentQueries, Defaults().MaxConcurrentQueries, c.MaxConcurrentQueries),
		MaxQueriesPerHour:      clamp(p.MaxQueriesPerHour, Defaults().MaxQueriesPerHour, c.MaxQueriesPerHour),
		MaxToolCallsPerMinute:  clamp(p.MaxToolCallsPerMinute, Defaults().MaxToolCallsPerMinute, c.MaxToolCallsPerMinute),
	}
	for _, t := range tools {
		e.Tools[t] = true
	}
	return e
}

func clamp(v, def, ceiling int) int {
	if v <= 0 {
		v = def
	}
	if ceiling > 0 && v > ceiling {
		v = ceiling
	}
	return v
}

func uuidSet(ids []string) map[string]bool {
	if ids == nil {
		return nil
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[strings.ToLower(id)] = true
	}
	return out
}

// Parse decodes a stored policy (JSONB) and applies the defaults.
func Parse(raw []byte) (Policy, error) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return Policy{Version: 1}, nil
	}
	var p Policy
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("agent policy: %w", err)
	}
	if p.Version == 0 {
		p.Version = 1
	}
	return p, nil
}

// Validate rejects a policy the gate could not enforce: unknown tools, bad
// UUIDs, a version it does not know, or a limit above the server ceiling.
func (p Policy) Validate(c Ceilings) error {
	if p.Version != 0 && p.Version != 1 {
		return fmt.Errorf("unsupported policy version %d", p.Version)
	}
	seen := make(map[string]bool, len(p.Tools))
	for _, t := range p.Tools {
		if !IsTool(t) {
			return fmt.Errorf("unknown tool %q", t)
		}
		if seen[t] {
			return fmt.Errorf("duplicate tool %q", t)
		}
		seen[t] = true
	}
	for _, list := range []struct {
		name string
		ids  []string
	}{{"catalogs", p.Catalogs}, {"engines", p.Engines}} {
		for _, id := range list.ids {
			if _, err := uuid.Parse(id); err != nil {
				return fmt.Errorf("%s: %q is not a UUID", list.name, id)
			}
		}
	}
	limits := []struct {
		name    string
		value   int
		ceiling int
	}{
		{"max_rows_returned", p.MaxRowsReturned, c.MaxRowsReturned},
		{"max_result_bytes", p.MaxResultBytes, c.MaxResultBytes},
		{"max_query_runtime_seconds", p.MaxQueryRuntimeSeconds, c.MaxQueryRuntimeSeconds},
		{"max_concurrent_queries", p.MaxConcurrentQueries, c.MaxConcurrentQueries},
		{"max_queries_per_hour", p.MaxQueriesPerHour, c.MaxQueriesPerHour},
		{"max_tool_calls_per_minute", p.MaxToolCallsPerMinute, c.MaxToolCallsPerMinute},
	}
	for _, l := range limits {
		if l.value < 0 {
			return fmt.Errorf("%s must not be negative", l.name)
		}
		if l.value > l.ceiling {
			return fmt.Errorf("%s exceeds the server maximum of %d", l.name, l.ceiling)
		}
	}
	return nil
}
