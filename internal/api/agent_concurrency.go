package api

import "context"

// agentConcurrencyKey carries the agent policy's per-agent concurrency limit
// from the MCP gate, which evaluated it, to RunQuery (#589).
type agentConcurrencyKey struct{}

// WithAgentMaxConcurrent tells RunQuery that the caller is an agent whose
// policy allows at most n concurrent runs. Admission enforces the limit; the
// run waits instead of being refused (#548, #589).
func WithAgentMaxConcurrent(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, agentConcurrencyKey{}, n)
}

func agentMaxConcurrent(ctx context.Context) int {
	n, _ := ctx.Value(agentConcurrencyKey{}).(int)
	return n
}

// agentScopeKey carries the agent policy's catalog and engine allow-lists
// from the MCP gate to RunQuery, which checks them against the target it
// resolves (#653): a binding names its own catalog and engine, which the gate
// did not see.
type agentScopeKey struct{}

type agentScope struct{ catalogs, engines map[string]bool }

// WithAgentScope tells RunQuery which catalogs and engines the agent's policy
// allows; an empty set allows any.
func WithAgentScope(ctx context.Context, catalogs, engines map[string]bool) context.Context {
	return context.WithValue(ctx, agentScopeKey{}, agentScope{catalogs: catalogs, engines: engines})
}

// agentScopeAllows reports whether the agent policy in ctx allows the
// resolved catalog and engine; true when ctx carries no policy.
func agentScopeAllows(ctx context.Context, catalogID, engineID string) bool {
	s, ok := ctx.Value(agentScopeKey{}).(agentScope)
	if !ok {
		return true
	}
	if len(s.catalogs) > 0 && !s.catalogs[catalogID] {
		return false
	}
	return len(s.engines) == 0 || s.engines[engineID]
}
