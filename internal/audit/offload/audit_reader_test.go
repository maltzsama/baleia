package offload

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

// TestColdAuditDocument: the cold object is lossless; it keeps the id and
// on_behalf_of the OpenSearch projection omits, so an event can be restored
// after its hot partition is dropped.
func TestColdAuditDocument(t *testing.T) {
	t.Parallel()
	e := export.Event{
		ID: 42, OccurredAt: time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC),
		ActorKind: "user", ActorID: "u1", Action: "catalog.update", ResourceType: "catalog",
		Outcome: "allowed", OnBehalfOf: "00000000-0000-0000-0000-000000000009",
		// A number past float64's exact range: decoding through any would
		// round it to 9007199254740992.
		Changes: json.RawMessage(`{"value":9007199254740993}`),
	}
	doc := ColdAuditDocument(e)
	if doc["id"] != int64(42) {
		t.Fatalf("id = %v, want 42", doc["id"])
	}
	if doc["on_behalf_of"] != "00000000-0000-0000-0000-000000000009" {
		t.Fatalf("on_behalf_of = %v", doc["on_behalf_of"])
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), "9007199254740993") {
		t.Fatalf("changes lost precision: %s", b)
	}
}
