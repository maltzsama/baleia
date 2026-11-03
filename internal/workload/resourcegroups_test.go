package workload

import (
	"encoding/json"
	"testing"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// TestResourceGroups_Generate covers the A-5.1 generator (#598): the root
// group, the optional ceiling, the per-class subgroups and the selectors.
func TestResourceGroups_Generate(t *testing.T) {
	t.Parallel()
	ceiling := 50
	got := Generate(map[queryengine.WorkloadClass]Capacity{
		queryengine.ClassInteractive: {MaxRunning: 20, MaxQueued: 200},
		queryengine.ClassHeavy:       {MaxRunning: 3, MaxQueued: 50},
		queryengine.ClassBackground:  {MaxRunning: 2, MaxQueued: 100},
	}, &ceiling)

	if err := Validate(got); err != nil {
		t.Fatalf("Validate = %v", err)
	}
	if len(got.RootGroups) != 1 {
		t.Fatalf("root groups = %d, want 1", len(got.RootGroups))
	}
	root := got.RootGroups[0]
	if root.Name != "baleia" {
		t.Errorf("root name = %q", root.Name)
	}
	if root.HardConcurrencyLimit == nil || *root.HardConcurrencyLimit != 50 {
		t.Errorf("root ceiling = %v, want 50", root.HardConcurrencyLimit)
	}
	if len(root.SubGroups) != 3 {
		t.Fatalf("subgroups = %d, want 3", len(root.SubGroups))
	}

	byName := map[string]SubGroup{}
	for _, sub := range root.SubGroups {
		byName[sub.Name] = sub
	}
	if byName["interactive"].HardConcurrencyLimit != 20 || byName["interactive"].MaxQueued != 200 {
		t.Errorf("interactive = %+v", byName["interactive"])
	}
	if byName["heavy"].HardConcurrencyLimit != 3 || byName["heavy"].MaxQueued != 50 {
		t.Errorf("heavy = %+v", byName["heavy"])
	}
	if byName["background"].HardConcurrencyLimit != 2 || byName["background"].MaxQueued != 100 {
		t.Errorf("background = %+v", byName["background"])
	}

	// Three class selectors plus the catch-all, in a deterministic order.
	if len(got.Selectors) != 4 {
		t.Fatalf("selectors = %d, want 4", len(got.Selectors))
	}
	if got.Selectors[0].ClientTags[0] != "baleia.class=interactive" || got.Selectors[0].Group != "baleia.interactive" {
		t.Errorf("first selector = %+v", got.Selectors[0])
	}
	last := got.Selectors[len(got.Selectors)-1]
	if len(last.ClientTags) != 0 || last.Group != "baleia.interactive" {
		t.Errorf("catch-all selector = %+v", last)
	}
}

// TestResourceGroups_NoCeiling leaves the root hardConcurrencyLimit unset.
func TestResourceGroups_NoCeiling(t *testing.T) {
	t.Parallel()
	got := Generate(map[queryengine.WorkloadClass]Capacity{
		queryengine.ClassInteractive: {MaxRunning: 4, MaxQueued: 20},
	}, nil)
	if got.RootGroups[0].HardConcurrencyLimit != nil {
		t.Errorf("ceiling = %v, want nil", got.RootGroups[0].HardConcurrencyLimit)
	}
}

// TestResourceGroups_ClosedClass keeps a class whose capacity is 0 as a
// closed group with its own selector, so its runs wait in Trino instead of
// falling through to the catch-all and an open class's capacity (#635).
func TestResourceGroups_ClosedClass(t *testing.T) {
	t.Parallel()
	got := Generate(map[queryengine.WorkloadClass]Capacity{
		queryengine.ClassInteractive: {MaxRunning: 4, MaxQueued: 20},
		queryengine.ClassHeavy:       {MaxRunning: 1, MaxQueued: 10},
		queryengine.ClassBackground:  {MaxRunning: 0, MaxQueued: 7},
	}, nil)

	if err := Validate(got); err != nil {
		t.Fatalf("Validate = %v", err)
	}
	subs := got.RootGroups[0].SubGroups
	if len(subs) != 3 {
		t.Fatalf("subgroups = %d, want 3", len(subs))
	}
	if bg := subs[2]; bg.Name != "background" || bg.HardConcurrencyLimit != 0 || bg.MaxQueued != 7 {
		t.Errorf("closed background group = %+v, want limit 0 and maxQueued 7", bg)
	}
	routed := false
	for _, sel := range got.Selectors {
		if len(sel.ClientTags) == 1 && sel.ClientTags[0] == "baleia.class=background" {
			routed = sel.Group == "baleia.background"
		}
	}
	if !routed {
		t.Errorf("background runs are not routed to their closed group: %+v", got.Selectors)
	}
	// The catch-all still routes to interactive, which is open.
	last := got.Selectors[len(got.Selectors)-1]
	if len(last.ClientTags) != 0 || last.Group != "baleia.interactive" {
		t.Errorf("catch-all = %+v", last)
	}
}

// TestResourceGroups_AllClosed keeps every class as a closed group and emits
// no catch-all, since there is no open class to route to.
func TestResourceGroups_AllClosed(t *testing.T) {
	t.Parallel()
	got := Generate(map[queryengine.WorkloadClass]Capacity{
		queryengine.ClassInteractive: {MaxRunning: 0},
		queryengine.ClassHeavy:       {MaxRunning: 0},
		queryengine.ClassBackground:  {MaxRunning: 0},
	}, nil)
	if err := Validate(got); err != nil {
		t.Fatalf("Validate = %v", err)
	}
	for _, sub := range got.RootGroups[0].SubGroups {
		if sub.HardConcurrencyLimit != 0 {
			t.Errorf("subgroup %q limit = %d, want 0", sub.Name, sub.HardConcurrencyLimit)
		}
	}
	for _, sel := range got.Selectors {
		if len(sel.ClientTags) == 0 {
			t.Errorf("catch-all emitted with every class closed: %+v", sel)
		}
	}
}

// TestResourceGroups_Marshal renders the document as valid JSON.
func TestResourceGroups_Marshal(t *testing.T) {
	t.Parallel()
	got := Generate(map[queryengine.WorkloadClass]Capacity{
		queryengine.ClassInteractive: {MaxRunning: 4, MaxQueued: 20},
	}, nil)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal = %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal = %v", err)
	}
	if _, ok := doc["rootGroups"]; !ok {
		t.Errorf("no rootGroups in %s", raw)
	}
	if _, ok := doc["selectors"]; !ok {
		t.Errorf("no selectors in %s", raw)
	}
}

// TestResourceGroups_ValidateRejectsNegativeLimit: 0 is a closed class, below
// it is a mistake.
func TestResourceGroups_ValidateRejectsNegativeLimit(t *testing.T) {
	t.Parallel()
	got := Generate(map[queryengine.WorkloadClass]Capacity{
		queryengine.ClassInteractive: {MaxRunning: 4, MaxQueued: 20},
	}, nil)
	got.RootGroups[0].SubGroups[0].HardConcurrencyLimit = -1
	if err := Validate(got); err == nil {
		t.Fatal("Validate accepted a negative hardConcurrencyLimit")
	}
}

// TestResourceGroups_ValidateRejects a selector to an unknown group.
func TestResourceGroups_ValidateRejects(t *testing.T) {
	t.Parallel()
	got := Generate(map[queryengine.WorkloadClass]Capacity{
		queryengine.ClassInteractive: {MaxRunning: 4, MaxQueued: 20},
	}, nil)
	got.Selectors = append(got.Selectors, Selector{Group: "baleia.ghost"})
	if err := Validate(got); err == nil {
		t.Fatal("Validate accepted a selector to an unknown group")
	}
}
