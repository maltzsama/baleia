package api

import (
	"context"

	agentmanage "github.com/galileostudio/baleia/internal/agents/manage"
	"github.com/galileostudio/baleia/internal/assistant"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// AgentHandlers serves agents, their policy and tokens, the assistant and the MCP surface (#365).
type AgentHandlers struct {
	*core
	// dialer is the SSRF-safe dialer for transient engines (#12).
	dialer *connections.Dialer
	// assistant owns assistant settings, agents and conversations (#62).
	assistant *assistant.Service
	// assistantRunner holds the runner of MCP-backed turns (and its SSE hub),
	// stored after the handlers are built; empty when not provisioned.
	assistantRunner *assistant.RunnerSlot
	// agents stores external agents and their tokens (#59).
	agents *agentmanage.Service
}

func newAgentHandlers(c *core, svc *assistant.Service, runner *assistant.RunnerSlot, dialer *connections.Dialer) *AgentHandlers {
	tx := func(ctx context.Context, fn func(agentmanage.TxStore) error) error {
		return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
	}
	var store agentmanage.Store = c.queries
	var agentTx agentmanage.Tx = tx
	if c.store != nil {
		r := repo.New(c.store)
		store, agentTx = r, r.TxAgents
	}
	return &AgentHandlers{core: c, assistant: svc, assistantRunner: runner, dialer: dialer, agents: agentmanage.New(store, agentTx)}
}
