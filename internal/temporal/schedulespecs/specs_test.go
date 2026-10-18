package schedulespecs

import (
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/temporal/schedules"
)

func TestCore(t *testing.T) {
	specs := Core(90 * time.Second)
	if len(specs) != 9 {
		t.Fatalf("Core returned %d specs, want 9", len(specs))
	}
	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if spec.ID == "" || spec.Workflow == nil || spec.Every <= 0 {
			t.Fatalf("invalid spec: %+v", spec)
		}
		if seen[spec.ID] {
			t.Fatalf("duplicate schedule id %q", spec.ID)
		}
		seen[spec.ID] = true
	}
	if !seen[schedules.MetadataSync] {
		t.Fatal("metadata sync schedule is missing")
	}
	for _, spec := range specs {
		if spec.ID == schedules.MetadataSync && spec.Every != 90*time.Second {
			t.Fatalf("metadata sync every = %s, want 90s", spec.Every)
		}
	}
}

func TestOptionalSchedules(t *testing.T) {
	if got := CatalogReconcile(time.Hour); got.ID != schedules.CatalogReconcile || got.Every != time.Hour || got.Workflow == nil {
		t.Fatalf("CatalogReconcile = %+v", got)
	}
	if got := AuditExport(time.Minute); got.ID != schedules.AuditExport || got.Every != time.Minute || got.Workflow == nil {
		t.Fatalf("AuditExport = %+v", got)
	}
}
