package workload

import "github.com/galileostudio/baleia/internal/queryengine"

// Origin is where a run came from. Classification looks at it before any
// admin rule (#399, ADR 0014).
type Origin string

const (
	// OriginEditor is a Run from the SQL workbench.
	OriginEditor Origin = "editor"
	// OriginAgent is a run made by an agent principal.
	OriginAgent Origin = "agent"
	// OriginExplore is a compiled Explore query or an Explore preview.
	OriginExplore Origin = "explore"
	// OriginRetriever is a retriever refresh.
	OriginRetriever Origin = "retriever"
	// OriginMetadata is metadata introspection through an engine.
	OriginMetadata Origin = "metadata"
	// OriginMaintenance is Iceberg maintenance.
	OriginMaintenance Origin = "maintenance"
)

// Decision is the class a run gets and the step that chose it. The reason is
// stored on the execution and shown in its details.
type Decision struct {
	Class  queryengine.WorkloadClass
	Reason string
}

// Classify is the first classification step: background work gets the
// background class, everything a person waits for is interactive. Admin rules,
// cost routing and the user downgrade are later steps and come after this.
func Classify(origin Origin) Decision {
	switch origin {
	case OriginAgent, OriginRetriever, OriginMetadata, OriginMaintenance:
		return Decision{Class: queryengine.ClassBackground, Reason: "origin:" + string(origin)}
	case OriginExplore:
		return Decision{Class: queryengine.ClassInteractive, Reason: "origin:explore"}
	default:
		return Decision{Class: queryengine.ClassInteractive, Reason: "default:editor"}
	}
}
