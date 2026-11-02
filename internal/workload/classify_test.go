package workload

import (
	"testing"

	"github.com/galileostudio/baleia/internal/queryengine"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		origin Origin
		class  queryengine.WorkloadClass
		reason string
	}{
		{OriginEditor, queryengine.ClassInteractive, "default:editor"},
		{OriginExplore, queryengine.ClassInteractive, "origin:explore"},
		{OriginAgent, queryengine.ClassBackground, "origin:agent"},
		{OriginRetriever, queryengine.ClassBackground, "origin:retriever"},
		{OriginMetadata, queryengine.ClassBackground, "origin:metadata"},
		{OriginMaintenance, queryengine.ClassBackground, "origin:maintenance"},
	}
	for _, c := range cases {
		got := Classify(c.origin)
		if got.Class != c.class || got.Reason != c.reason {
			t.Errorf("Classify(%q) = %+v, want {%s %q}", c.origin, got, c.class, c.reason)
		}
		if !got.Class.Valid() {
			t.Errorf("Classify(%q) returned an invalid class %q", c.origin, got.Class)
		}
	}
}

func TestClassifyUnknownOriginIsInteractive(t *testing.T) {
	t.Parallel()
	if got := Classify(Origin("something-new")); got.Class != queryengine.ClassInteractive {
		t.Errorf("unknown origin class = %q, want interactive", got.Class)
	}
}

func TestDecide_RuleOrderAndMatcher(t *testing.T) {
	t.Parallel()
	in := RuleInput{WorkspaceID: "ws-1", Role: "member", Origin: OriginEditor, Statements: 3}
	rules := []Rule{
		{ID: "first", Enabled: true, Match: RuleMatch{WorkspaceID: "other"}, Class: queryengine.ClassHeavy, Weight: 1},
		{ID: "second", Enabled: true, Match: RuleMatch{WorkspaceID: "ws-1"}, Class: queryengine.ClassBackground, Weight: 4},
		{ID: "third", Enabled: true, Match: RuleMatch{WorkspaceID: "ws-1"}, Class: queryengine.ClassInteractive, Weight: 1},
	}
	got := Decide(OriginEditor, in, rules)
	if got.Class != queryengine.ClassBackground || got.Reason != "rule:second" || got.Weight != 4 {
		t.Fatalf("Decide = %+v, want the first matching rule", got)
	}
}

func TestDecide_DisabledRuleIsSkipped(t *testing.T) {
	t.Parallel()
	in := RuleInput{WorkspaceID: "ws-1", Origin: OriginEditor}
	rules := []Rule{
		{ID: "off", Enabled: false, Match: RuleMatch{WorkspaceID: "ws-1"}, Class: queryengine.ClassBackground, Weight: 1},
	}
	got := Decide(OriginEditor, in, rules)
	if got.Class != queryengine.ClassInteractive || got.Reason != "default:editor" {
		t.Fatalf("Decide = %+v, want the default", got)
	}
}

func TestDecide_MatchFields(t *testing.T) {
	t.Parallel()
	in := RuleInput{
		WorkspaceID: "ws-1", TeamID: "team-1", Role: "member", UserID: "user-1",
		AgentID: "agent-1", EngineID: "engine-1", Origin: OriginAgent,
		SavedQueryID: "saved-1", Statements: 5,
	}
	base := RuleMatch{
		WorkspaceID: "ws-1", TeamID: "team-1", Role: "member", UserID: "user-1",
		AgentID: "agent-1", EngineID: "engine-1", Origin: OriginAgent,
		SavedQueryID: "saved-1", MinStatements: 1, MaxStatements: 10,
	}
	rule := Rule{ID: "match", Enabled: true, Match: base, Class: queryengine.ClassHeavy, Weight: 2}
	if got := Decide(OriginAgent, in, []Rule{rule}); got.Reason != "rule:match" {
		t.Fatalf("a fully matching rule should match, got %+v", got)
	}

	for _, mutate := range []func(*RuleMatch){
		func(m *RuleMatch) { m.WorkspaceID = "other" },
		func(m *RuleMatch) { m.TeamID = "other" },
		func(m *RuleMatch) { m.Role = "admin" },
		func(m *RuleMatch) { m.UserID = "other" },
		func(m *RuleMatch) { m.AgentID = "other" },
		func(m *RuleMatch) { m.EngineID = "other" },
		func(m *RuleMatch) { m.Origin = OriginRetriever },
		func(m *RuleMatch) { m.SavedQueryID = "other" },
		func(m *RuleMatch) { m.MinStatements = 6 },
		func(m *RuleMatch) { m.MaxStatements = 4 },
	} {
		m := base
		mutate(&m)
		got := Decide(OriginAgent, in, []Rule{{ID: "no", Enabled: true, Match: m, Class: queryengine.ClassHeavy, Weight: 1}})
		if got.Reason != "origin:agent" {
			t.Fatalf("rule with a different field matched: %+v", got)
		}
	}
}

func TestDecide_MaxClassClamps(t *testing.T) {
	t.Parallel()
	in := RuleInput{WorkspaceID: "ws-1", Origin: OriginEditor}
	rule := Rule{ID: "cap", Enabled: true, Match: RuleMatch{WorkspaceID: "ws-1"}, Class: queryengine.ClassInteractive, MaxClass: queryengine.ClassHeavy, Weight: 1}
	got := Decide(OriginEditor, in, []Rule{rule})
	if got.Class != queryengine.ClassHeavy {
		t.Fatalf("max_class should cap an interactive class to heavy, got %+v", got)
	}
	// max_class never raises a lower class.
	rule.Class = queryengine.ClassBackground
	if got := Decide(OriginEditor, in, []Rule{rule}); got.Class != queryengine.ClassBackground {
		t.Fatalf("max_class should not raise background, got %+v", got)
	}
}

func TestParseRuleMatchRejectsUnknownOrigin(t *testing.T) {
	t.Parallel()
	if _, err := ParseRuleMatch([]byte(`{"origin":"nope"}`)); err == nil {
		t.Fatal("an unknown origin must be rejected")
	}
}

func TestClassRankAndClamp(t *testing.T) {
	t.Parallel()
	rank := func(c queryengine.WorkloadClass) int { return ClassRank(c) }
	if !(rank(queryengine.ClassInteractive) < rank(queryengine.ClassHeavy) && rank(queryengine.ClassHeavy) < rank(queryengine.ClassBackground)) {
		t.Fatal("class priority order is wrong")
	}
	if got := ClampToMax(queryengine.ClassInteractive, queryengine.ClassBackground); got != queryengine.ClassBackground {
		t.Fatalf("ClampToMax = %q, want background", got)
	}
	if got := ClampToMax(queryengine.ClassBackground, queryengine.ClassInteractive); got != queryengine.ClassBackground {
		t.Fatalf("ClampToMax = %q, want background", got)
	}
}
