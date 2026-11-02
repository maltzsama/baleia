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
