package queryengine

// WorkloadClass is the global kind of work a run belongs to (#399, ADR 0014).
// Policy speaks in classes; the capacity that applies is configured per engine.
type WorkloadClass string

const (
	// ClassInteractive is editor work that is cheap, table previews, Explore
	// and autocomplete introspection.
	ClassInteractive WorkloadClass = "interactive"
	// ClassHeavy is expensive editor work, large scripts and exports.
	ClassHeavy WorkloadClass = "heavy"
	// ClassBackground is retriever refreshes, metadata introspection, Iceberg
	// maintenance and agents by default.
	ClassBackground WorkloadClass = "background"
)

// Valid reports whether the class is one of the three Baleia knows.
func (c WorkloadClass) Valid() bool {
	switch c {
	case ClassInteractive, ClassHeavy, ClassBackground:
		return true
	default:
		return false
	}
}

// QueryPriority maps the class to Trino's query_priority session property.
//
// Trino documents it as "The priority of queries. Larger numbers are higher
// priority" (trino-main SystemSessionProperties, verified on 484), so the
// interactive class must carry the largest number. The mapping is deliberately
// coarse: it is a hint to a `query_priority` resource-group policy, not the
// admission decision, which Baleia makes itself.
func (c WorkloadClass) QueryPriority() (int, bool) {
	switch c {
	case ClassInteractive:
		return 10, true
	case ClassHeavy:
		return 5, true
	case ClassBackground:
		return 1, true
	default:
		return 0, false
	}
}
