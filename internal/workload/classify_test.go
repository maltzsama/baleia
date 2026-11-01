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
