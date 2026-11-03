package workload

import (
	"fmt"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// Resource groups (A-5.1, #598): Baleia renders Trino's resource-groups.json
// from the same capacities and ceiling it uses for its own admission, so
// Trino's queue and Baleia's queue agree. The generator is pure: it takes the
// configured capacities and the optional engine ceiling and returns the
// document, never touching the database or Trino.
//
// The shape follows the sample in docs/operations.md: one root `baleia` group,
// one subgroup per open class, and selectors on the `baleia.class` client tag
// with a catch-all that routes unclassified sessions to `interactive`.

// rootGroupName is the root resource group every Baleia class lives under.
const rootGroupName = "baleia"

// softMemoryLimit is the default soft memory limit of the root group. Trino
// applies it to the root and, when a subgroup sets none, the subgroup inherits
// it. The value is a placeholder that operators tune; Baleia does not schedule
// memory itself (ADR 0001).
const softMemoryLimit = "80%"

// classOrder is the fixed order subgroups and selectors are emitted in, so the
// output is deterministic.
var classOrder = []queryengine.WorkloadClass{
	queryengine.ClassInteractive,
	queryengine.ClassHeavy,
	queryengine.ClassBackground,
}

// ResourceGroups is the generated Trino resource-groups.json document.
type ResourceGroups struct {
	RootGroups []RootGroup `json:"rootGroups"`
	Selectors  []Selector  `json:"selectors"`
}

// RootGroup is the single `baleia` group that owns the per-class subgroups.
type RootGroup struct {
	Name                 string     `json:"name"`
	SoftMemoryLimit      string     `json:"softMemoryLimit"`
	HardConcurrencyLimit *int       `json:"hardConcurrencyLimit,omitempty"`
	SubGroups            []SubGroup `json:"subGroups"`
}

// SubGroup is one class's queue, with its concurrency and queued limits.
type SubGroup struct {
	Name                 string `json:"name"`
	HardConcurrencyLimit int    `json:"hardConcurrencyLimit"`
	MaxQueued            int    `json:"maxQueued"`
}

// Selector routes a session to a group. The empty ClientTags selector is the
// catch-all, so it is always last.
type Selector struct {
	ClientTags []string `json:"clientTags,omitempty"`
	Group      string   `json:"group"`
}

// Generate renders the document. ceiling, when non-nil and positive, becomes
// the root hardConcurrencyLimit. A class whose capacity is 0 (MaxRunning == 0)
// is closed: it keeps its subgroup and selector with a hardConcurrencyLimit of
// 0, so Trino holds its runs (up to maxQueued) as Baleia's own queue would,
// instead of the catch-all routing them into an open class's capacity (#635).
func Generate(classes map[queryengine.WorkloadClass]Capacity, ceiling *int) ResourceGroups {
	root := RootGroup{Name: rootGroupName, SoftMemoryLimit: softMemoryLimit}
	if ceiling != nil && *ceiling > 0 {
		limit := *ceiling
		root.HardConcurrencyLimit = &limit
	}

	out := ResourceGroups{}
	for _, class := range classOrder {
		capacity, ok := classes[class]
		if !ok {
			capacity = Defaults("federated")[class]
		}
		root.SubGroups = append(root.SubGroups, SubGroup{
			Name:                 string(class),
			HardConcurrencyLimit: max(capacity.MaxRunning, 0), // 0: the class is closed
			MaxQueued:            capacity.MaxQueued,
		})
		out.Selectors = append(out.Selectors, Selector{
			ClientTags: []string{"baleia.class=" + string(class)},
			Group:      rootGroupName + "." + string(class),
		})
	}

	// The catch-all sends an unclassified session (one without a baleia.class
	// tag) to interactive, or to the first open class when interactive itself
	// is closed. With every class closed there is nothing to route to, so no
	// catch-all is emitted.
	if fallback := firstOpenClass(classes); fallback != "" {
		out.Selectors = append(out.Selectors, Selector{Group: rootGroupName + "." + fallback})
	}

	out.RootGroups = []RootGroup{root}
	return out
}

// firstOpenClass is the class an unclassified session falls back to, in the
// fixed order. Empty when every class is closed.
func firstOpenClass(classes map[queryengine.WorkloadClass]Capacity) string {
	for _, class := range classOrder {
		capacity, ok := classes[class]
		if !ok {
			capacity = Defaults("federated")[class]
		}
		if capacity.MaxRunning > 0 {
			return string(class)
		}
	}
	return ""
}

// Validate checks the document's structural invariants: one root group, every
// subgroup named and reachable through the root, every selector pointing at an
// existing group, and no negative limit (0 is a closed class). It is the gate
// the caller runs before writing the document anywhere.
func Validate(rg ResourceGroups) error {
	if len(rg.RootGroups) != 1 {
		return fmt.Errorf("resource groups: want exactly one root group, got %d", len(rg.RootGroups))
	}
	root := rg.RootGroups[0]
	if root.Name != rootGroupName {
		return fmt.Errorf("resource groups: root group must be named %q", rootGroupName)
	}
	if root.HardConcurrencyLimit != nil && *root.HardConcurrencyLimit < 0 {
		return fmt.Errorf("resource groups: root hardConcurrencyLimit must not be negative")
	}

	groups := map[string]bool{root.Name: true}
	seen := map[string]bool{}
	for _, sub := range root.SubGroups {
		if sub.Name == "" {
			return fmt.Errorf("resource groups: a subgroup has no name")
		}
		if seen[sub.Name] {
			return fmt.Errorf("resource groups: duplicate subgroup %q", sub.Name)
		}
		seen[sub.Name] = true
		if sub.HardConcurrencyLimit < 0 {
			return fmt.Errorf("resource groups: subgroup %q has a negative hardConcurrencyLimit", sub.Name)
		}
		if sub.MaxQueued < 0 {
			return fmt.Errorf("resource groups: subgroup %q has a negative maxQueued", sub.Name)
		}
		groups[root.Name+"."+sub.Name] = true
	}

	for i, sel := range rg.Selectors {
		if !groups[sel.Group] {
			return fmt.Errorf("resource groups: selector %d points at unknown group %q", i, sel.Group)
		}
	}
	return nil
}
