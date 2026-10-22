package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/jackc/pgx/v5/pgtype"
)

func findStatusItem(t *testing.T, res SystemStatusResource, name string) SystemStatusItem {
	t.Helper()
	if res.Items == nil {
		t.Fatalf("section has no items")
	}
	for _, item := range *res.Items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("item %q is missing from %+v", name, *res.Items)
	return SystemStatusItem{}
}

// TestSystemStatus_ScheduleMissing: a schedule Temporal does not know is
// reported as `missing`, never blank.
func TestSystemStatus_ScheduleMissing(t *testing.T) {
	h := &Handlers{schema: unreachableSchema{}}
	h.systemStatusSchedule = func(_ context.Context, id string) (SystemStatusItemStatus, string) {
		if id == schedules.MetadataSync {
			return SystemStatusItemStatusMissing, "schedule_missing"
		}
		return SystemStatusItemStatusOk, ""
	}

	res := h.schedulesStatus(context.Background())
	item := findStatusItem(t, res, schedules.MetadataSync)
	if item.Status != SystemStatusItemStatusMissing {
		t.Fatalf("metadata-sync status = %q, want missing", item.Status)
	}
	if item.Detail == nil || *item.Detail != "schedule_missing" {
		t.Fatalf("detail = %v", item.Detail)
	}
	if ok := findStatusItem(t, res, schedules.TokenCleanup); ok.Status != SystemStatusItemStatusOk {
		t.Fatalf("token-cleanup status = %q, want ok", ok.Status)
	}
}

// TestSystemStatus_EngineHealth: the engines section reports the probed health
// of each engine.
func TestSystemStatus_EngineHealth(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()

	var connID pgtype.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO catalog_connections (name, type, url)
		VALUES ('status-conn', 'polaris', 'https://polaris.example.test') RETURNING id`).Scan(&connID); err != nil {
		t.Fatal(err)
	}
	var engineID pgtype.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO engines (name, kind, class, connection_id)
		VALUES ('status-engine', 'trino', 'federated', $1) RETURNING id`, connID).Scan(&engineID); err != nil {
		t.Fatal(err)
	}

	f.h.systemStatusEngine = func(_ context.Context, e db.Engine) (SystemStatusItemStatus, string) {
		if e.ID == engineID {
			return SystemStatusItemStatusUnreachable, "engine_unreachable"
		}
		return SystemStatusItemStatusOk, ""
	}

	res := f.h.enginesStatus(ctx)
	item := findStatusItem(t, res, "status-engine")
	if item.Status != SystemStatusItemStatusUnreachable {
		t.Fatalf("engine status = %q, want unreachable", item.Status)
	}
}

// TestSystemStatus_PlatformSections: every new section probes a real database
// without error and reports its components.
func TestSystemStatus_PlatformSections(t *testing.T) {
	f := newAuditFixture(t)
	resp, err := f.h.GetSystemStatus(ctxAs(f.admin, "admin"), GetSystemStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(GetSystemStatus200JSONResponse)
	if !found {
		t.Fatalf("response = %T, want 200", resp)
	}
	for name, section := range map[string]SystemStatusResource{
		"schedules": ok.Schedules, "engines": ok.Engines, "bindings": ok.Bindings,
		"metadata": ok.Metadata, "reconcile": ok.Reconcile, "audit_export": ok.AuditExport,
		"plugin": ok.Plugin,
	} {
		if section.Status == "" {
			t.Errorf("%s section has an empty status", name)
		}
		if section.Items == nil {
			t.Errorf("%s section has no items", name)
		}
	}
	if ok.Schedules.ErrorCode == nil || *ok.Schedules.ErrorCode != "temporal_unavailable" {
		t.Fatalf("schedules without Temporal = %+v", ok.Schedules)
	}
}
