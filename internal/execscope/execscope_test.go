package execscope

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeStore struct {
	rows []db.ListWorkspaceTargetsRow
}

func (f *fakeStore) ListWorkspaceTargets(context.Context, pgtype.UUID) ([]db.ListWorkspaceTargetsRow, error) {
	return f.rows, nil
}

func id(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

func row(engine byte, catalog string, binding, engineEnabled, catalogEnabled bool) db.ListWorkspaceTargetsRow {
	return db.ListWorkspaceTargetsRow{
		EngineID:          id(engine),
		EngineCatalogName: catalog,
		BindingEnabled:    binding,
		EngineEnabled:     engineEnabled,
		CatalogEnabled:    catalogEnabled,
	}
}

// TestForFiltersToEngine: the scope is the enabled catalogs of one engine, and
// a binding/engine/catalog that is disabled never counts (ADR 0018 §5).
func TestForFiltersToEngine(t *testing.T) {
	t.Parallel()
	store := &fakeStore{rows: []db.ListWorkspaceTargetsRow{
		row(1, "lake", true, true, true),
		row(1, "raw", true, true, false),  // catalog disabled
		row(1, "off", false, true, true),  // binding disabled
		row(2, "other", true, true, true), // another engine
	}}
	got, err := For(store, id(9), id(1))(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lake"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("scope = %v, want %v", got, want)
	}
}

// TestForRereads: the source re-reads the store on every call, so a revocation
// is reflected by the next statement, not the next run.
func TestForRereads(t *testing.T) {
	t.Parallel()
	store := &fakeStore{rows: []db.ListWorkspaceTargetsRow{row(1, "lake", true, true, true)}}
	src := For(store, id(9), id(1))

	if got, _ := src(context.Background()); len(got) != 1 {
		t.Fatalf("scope = %v, want one catalog", got)
	}
	store.rows = nil
	if got, _ := src(context.Background()); len(got) != 0 {
		t.Fatalf("scope = %v, want empty after revocation", got)
	}
}
