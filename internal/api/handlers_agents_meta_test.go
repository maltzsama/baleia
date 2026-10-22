package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeEngineSource struct{ readOnly queryengine.ReadOnlySupport }

func (f fakeEngineSource) Get(context.Context, pgtype.UUID) (queryengine.Engine, error) {
	return fake.New(fake.Options{ReadOnly: f.readOnly}), nil
}

func (f fakeEngineSource) DefaultTrino(context.Context) (queryengine.Engine, error) {
	return fake.New(fake.Options{ReadOnly: f.readOnly}), nil
}

func TestAgentsMeta(t *testing.T) {
	f := newAgentsFixture(t, true)
	got, err := f.h.GetAgentsMeta(f.owner, GetAgentsMetaRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := got.(GetAgentsMeta200JSONResponse)
	if !ok {
		t.Fatalf("meta = %T", got)
	}
	if meta.McpUrl != "/mcp" {
		t.Errorf("mcp_url = %q, want /mcp when PUBLIC_URL is empty", meta.McpUrl)
	}
	if meta.TokenDefaultTtlDays < 1 || meta.TokenMaxTtlDays < meta.TokenDefaultTtlDays {
		t.Errorf("ttl days = %d/%d", meta.TokenDefaultTtlDays, meta.TokenMaxTtlDays)
	}
	if len(meta.Tools) != len(agentpolicy.AllTools) {
		t.Fatalf("tools = %d, want %d", len(meta.Tools), len(agentpolicy.AllTools))
	}
	for _, tool := range meta.Tools {
		if tool.Label == "" || tool.Description == "" {
			t.Errorf("tool %q has no label/description", tool.Name)
		}
		if tool.Name == agentpolicy.ToolCancelExecution && tool.ReadOnly {
			t.Error("cancel_execution must be flagged as changing state")
		}
	}
}

func TestAgentEligibilityPolicyExclusion(t *testing.T) {
	catalogID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	engineID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	h := testHandlers(handlerFields{})
	engine := db.Engine{Name: "prod", Kind: "trino", Enabled: true}

	excludedCatalog := agentpolicy.Effective{Catalogs: map[string]bool{"00000000-0000-0000-0000-000000000009": true}}
	got := h.engineEligibility(t.Context(), excludedCatalog, catalogID, engineID, engine)
	if got.Status != AgentEngineEligibilityStatusExcludedByPolicy {
		t.Errorf("excluded catalog status = %q", got.Status)
	}

	excludedEngine := agentpolicy.Effective{Engines: map[string]bool{"00000000-0000-0000-0000-000000000009": true}}
	got = h.engineEligibility(t.Context(), excludedEngine, catalogID, engineID, engine)
	if got.Status != AgentEngineEligibilityStatusExcludedByPolicy {
		t.Errorf("excluded engine status = %q", got.Status)
	}

	// With no engine source the handler cannot probe read-only support; the
	// policy exclusion already returned above, so the default is ok.
	got = h.engineEligibility(t.Context(), agentpolicy.Effective{}, catalogID, engineID, engine)
	if got.Status != AgentEngineEligibilityStatusOk {
		t.Errorf("unrestricted status = %q", got.Status)
	}
}

func TestAgentEligibilityReadOnlyNotEnforced(t *testing.T) {
	h := testHandlers(handlerFields{engines: fakeEngineSource{readOnly: queryengine.ReadOnlyNone}})
	catalogID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	engineID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	got := h.engineEligibility(t.Context(), agentpolicy.Effective{}, catalogID, engineID, db.Engine{Name: "prod", Kind: "postgresql", Enabled: true})
	if got.Status != AgentEngineEligibilityStatusReadOnlyNotEnforced {
		t.Fatalf("status = %q, want read_only_not_enforced", got.Status)
	}
}
