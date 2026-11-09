package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
)

// workloadRuleCtx is an administrator without a user row: the rule's
// created_by stays NULL, so the CRUD does not need a fixture user.
func workloadRuleCtx(role string) context.Context {
	return context.WithValue(context.Background(), auth.ContextKeyRole, role)
}

func TestWorkloadRulesCRUDAndTest(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := repo.New(pool)
	mem := &memAudit{}
	h := testAPI(handlerFields{queries: q, tx: directTx{q: q}, audit: mem})
	admin := workloadRuleCtx(authzRoleAdmin)

	// Non-admins are refused.
	if resp, err := h.ListWorkloadRules(workloadRuleCtx("member"), ListWorkloadRulesRequestObject{}); err != nil {
		t.Fatal(err)
	} else if _, ok := resp.(ListWorkloadRules403JSONResponse); !ok {
		t.Fatalf("a member must not list workload rules, got %T", resp)
	}

	origin := WorkloadRuleMatchOrigin("agent")
	weight := 2
	created, err := h.CreateWorkloadRule(admin, CreateWorkloadRuleRequestObject{Body: &CreateWorkloadRuleJSONRequestBody{
		Class:  CreateWorkloadRuleRequestClass("heavy"),
		Match:  &WorkloadRuleMatch{Origin: &origin},
		Weight: &weight,
	}})
	if err != nil {
		t.Fatal(err)
	}
	rule, ok := created.(CreateWorkloadRule201JSONResponse)
	if !ok {
		t.Fatalf("CreateWorkloadRule = %T", created)
	}
	if rule.Class != WorkloadRuleClassHeavy || rule.Weight != 2 || rule.Position != 1 {
		t.Fatalf("created rule = %+v", rule)
	}

	list, ok := mustListRules(admin, t, h).(ListWorkloadRules200JSONResponse)
	if !ok || len(list) != 1 || list[0].Id != rule.Id {
		t.Fatalf("ListWorkloadRules = %#v", list)
	}

	// :test names the rule that decided and its class.
	tested, err := h.TestWorkloadRule(admin, TestWorkloadRuleRequestObject{Body: &TestWorkloadRuleJSONRequestBody{
		Origin: WorkloadRuleTestRequestOrigin("agent"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := tested.(TestWorkloadRule200JSONResponse)
	if !ok || result.Class != WorkloadRuleTestResultClass("heavy") || result.Weight != 2 {
		t.Fatalf("TestWorkloadRule = %#v", tested)
	}
	if result.RuleId == nil || *result.RuleId != rule.Id {
		t.Fatalf("TestWorkloadRule rule_id = %v, want %v", result.RuleId, rule.Id)
	}

	// Update keeps the fields the patch omits.
	updated, err := h.UpdateWorkloadRule(admin, UpdateWorkloadRuleRequestObject{
		RuleId: rule.Id,
		Body:   &UpdateWorkloadRuleJSONRequestBody{Class: ptr(UpdateWorkloadRuleRequestClass("background"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedRule, ok := updated.(UpdateWorkloadRule200JSONResponse)
	if !ok || updatedRule.Class != WorkloadRuleClassBackground || updatedRule.Weight != 2 {
		t.Fatalf("UpdateWorkloadRule = %#v", updated)
	}

	// The rule now matches and classifies as background.
	tested, err = h.TestWorkloadRule(admin, TestWorkloadRuleRequestObject{Body: &TestWorkloadRuleJSONRequestBody{
		Origin: WorkloadRuleTestRequestOrigin("agent"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result, ok := tested.(TestWorkloadRule200JSONResponse); !ok || result.Class != WorkloadRuleTestResultClass("background") {
		t.Fatalf("TestWorkloadRule = %#v", tested)
	}

	deleted, err := h.DeleteWorkloadRule(admin, DeleteWorkloadRuleRequestObject{RuleId: rule.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := deleted.(DeleteWorkloadRule204Response); !ok {
		t.Fatalf("DeleteWorkloadRule = %T", deleted)
	}
	again, err := h.DeleteWorkloadRule(admin, DeleteWorkloadRuleRequestObject{RuleId: rule.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.(DeleteWorkloadRule404JSONResponse); !ok {
		t.Fatalf("the second delete = %T, want 404", again)
	}

	events := mem.take()
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want create, update and delete", len(events))
	}
	want := []audit.Action{audit.WorkloadRuleCreate, audit.WorkloadRuleUpdate, audit.WorkloadRuleDelete}
	for i, ev := range events {
		if ev.Action != want[i] {
			t.Errorf("event %d = %s, want %s", i, ev.Action, want[i])
		}
	}
}

// The role constant is spelled here so the test reads like the handler's check.
const authzRoleAdmin = "admin"

func mustListRules(ctx context.Context, t *testing.T, h *API) ListWorkloadRulesResponseObject {
	t.Helper()
	resp, err := h.ListWorkloadRules(ctx, ListWorkloadRulesRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
