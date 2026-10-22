package api

import (
	"github.com/galileostudio/baleia/internal/assistant"
	"github.com/galileostudio/baleia/internal/connections"
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
}
