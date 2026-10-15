package bindings_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeBinder fails the operations listed in fail and confirms unless told not to.
type fakeBinder struct {
	fail      map[string]error
	unconfirm string
	calls     []string
	observed  []bindings.Observed
}

func (f *fakeBinder) EngineKind() string                      { return "fake" }
func (f *fakeBinder) Supports() map[connections.Kind][]string { return nil }
func (f *fakeBinder) ProtectedKeyPatterns() []string          { return nil }
func (f *fakeBinder) ReservedNames() []string                 { return []string{"system"} }
func (f *fakeBinder) Project(bindings.BindingSpec, connections.Config) (bindings.Projection, error) {
	return bindings.Projection{}, nil
}
func (f *fakeBinder) op(name string, catalog string) error {
	f.calls = append(f.calls, name+" "+catalog)
	return f.fail[name]
}
func (f *fakeBinder) Create(_ context.Context, _ bindings.EngineRef, p bindings.Projection) error {
	return f.op("create", p.EngineCatalog)
}
func (f *fakeBinder) Replace(_ context.Context, _ bindings.EngineRef, p bindings.Projection) error {
	return f.op("replace", p.EngineCatalog)
}
func (f *fakeBinder) Rename(_ context.Context, _ bindings.EngineRef, old string, p bindings.Projection) error {
	return f.op("rename", p.EngineCatalog)
}
func (f *fakeBinder) Drop(_ context.Context, _ bindings.EngineRef, name string) error {
	return f.op("drop", name)
}
func (f *fakeBinder) Confirm(context.Context, bindings.EngineRef, bindings.Projection) (bindings.Confirmation, error) {
	if f.unconfirm != "" {
		return bindings.Confirmation{Reason: f.unconfirm}, nil
	}
	return bindings.Confirmation{Confirmed: true}, nil
}
func (f *fakeBinder) Observe(context.Context, bindings.EngineRef) ([]bindings.Observed, error) {
	return f.observed, nil
}

type codeErr struct{ code string }

func (e codeErr) Error() string { return "engine says " + e.code }
func (e codeErr) Code() string  { return e.code }

// store keeps one binding and enforces the transition guards.
type store struct {
	b            db.CatalogBinding
	observations map[string]string // name -> mode
	absentKeep   []string
	adopted      []string
}

func (s *store) TransitionBinding(_ context.Context, a db.TransitionBindingParams) (db.CatalogBinding, error) {
	if a.ID != s.b.ID || !slices.Contains(a.FromStates, s.b.LifecycleState) {
		return db.CatalogBinding{}, pgx.ErrNoRows
	}
	s.b.LifecycleState, s.b.LastErrorCode, s.b.LastErrorMessage = a.ToState, a.ErrorCode, a.ErrorMessage
	if a.EngineCatalogName != nil {
		s.b.EngineCatalogName = *a.EngineCatalogName
	}
	return s.b, nil
}
func (s *store) ManagedBindingNamesOfEngine(context.Context, pgtype.UUID) ([]string, error) {
	return []string{"sales"}, nil
}
func (s *store) UpsertEngineCatalogObservation(_ context.Context, a db.UpsertEngineCatalogObservationParams) error {
	s.observations[a.CatalogName] = a.Mode
	return nil
}
func (s *store) MarkEngineCatalogsAbsent(_ context.Context, a db.MarkEngineCatalogsAbsentParams) error {
	s.absentKeep = a.PresentNames
	return nil
}
func (s *store) UpsertNativeCatalog(_ context.Context, a db.UpsertNativeCatalogParams) (db.Catalog, error) {
	return db.Catalog{ID: pgtype.UUID{Bytes: [16]byte{9}, Valid: true}, Name: a.Name}, nil
}
func (s *store) UpsertAdoptedBinding(_ context.Context, a db.UpsertAdoptedBindingParams) error {
	s.adopted = append(s.adopted, a.EngineCatalogName)
	return nil
}

func setup(state string) (*store, *fakeBinder, bindings.Lifecycle) {
	st := &store{b: db.CatalogBinding{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, EngineCatalogName: "sales", Mode: "managed", LifecycleState: state},
		observations: map[string]string{}}
	fb := &fakeBinder{fail: map[string]error{}}
	return st, fb, bindings.Lifecycle{Store: st, Binder: fb, ConnectionID: pgtype.UUID{Bytes: [16]byte{7}, Valid: true}}
}

var proj = bindings.Projection{EngineCatalog: "sales", Version: "v2"}

func TestProvision(t *testing.T) {
	st, _, l := setup(bindings.StateProvisioning)
	b, err := l.Provision(context.Background(), st.b, proj)
	if err != nil || b.LifecycleState != bindings.StateActive || b.LastErrorCode != nil {
		t.Fatalf("ok: %+v, %v", b, err)
	}

	st, fb, l := setup(bindings.StateProvisioning)
	fb.fail["create"] = codeErr{"catalog_already_exists"}
	b, err = l.Provision(context.Background(), st.b, proj)
	if err != nil || b.LifecycleState != bindings.StateError || *b.LastErrorCode != "catalog_already_exists" {
		t.Fatalf("ddl failure: %+v, %v", b, err)
	}

	st, fb, l = setup(bindings.StateProvisioning)
	fb.unconfirm = "plugin_not_confirmed"
	b, _ = l.Provision(context.Background(), st.b, proj)
	if b.LifecycleState != bindings.StateError || *b.LastErrorCode != "plugin_not_confirmed" {
		t.Fatalf("unconfirmed: %+v", b)
	}

	// The guard: a binding that is not provisioning is a concurrent change.
	st, _, l = setup(bindings.StateActive)
	if _, err := l.Provision(context.Background(), st.b, proj); !errors.Is(err, bindings.ErrConcurrentChange) {
		t.Fatalf("guard: %v", err)
	}
}

func TestRename(t *testing.T) {
	ctx := context.Background()
	st, fb, l := setup(bindings.StateUpdating)
	st.b.EngineCatalogName = "sales_eu" // the new name is written before the procedure
	b, err := l.Rename(ctx, st.b, "sales", bindings.Projection{EngineCatalog: "sales_eu"})
	if err != nil || b.LifecycleState != bindings.StateActive || !slices.Equal(fb.calls, []string{"create sales_eu", "drop sales"}) {
		t.Fatalf("rename: %+v %v calls %v", b, err, fb.calls)
	}

	// The new name cannot be created: the binding keeps its old name, active,
	// with the error.
	st, fb, l = setup(bindings.StateUpdating)
	st.b.EngineCatalogName = "sales_eu"
	fb.fail["create"] = errors.New("boom")
	b, err = l.Rename(ctx, st.b, "sales", bindings.Projection{EngineCatalog: "sales_eu"})
	if err != nil || b.LifecycleState != bindings.StateActive || b.EngineCatalogName != "sales" || *b.LastErrorCode != "engine_error" {
		t.Fatalf("failed rename: %+v %v", b, err)
	}

	// The old name cannot be dropped: active under the new name, error says so.
	st, fb, l = setup(bindings.StateUpdating)
	st.b.EngineCatalogName = "sales_eu"
	fb.fail["drop"] = errors.New("busy")
	b, _ = l.Rename(ctx, st.b, "sales", bindings.Projection{EngineCatalog: "sales_eu"})
	if b.LifecycleState != bindings.StateActive || b.EngineCatalogName != "sales_eu" || *b.LastErrorCode != "old_name_not_dropped" {
		t.Fatalf("old name left: %+v", b)
	}
}

func TestUpdateSyncDeprovision(t *testing.T) {
	ctx := context.Background()
	st, fb, l := setup(bindings.StateUpdating)
	fb.fail["replace"] = errors.New("boom")
	b, _ := l.Update(ctx, st.b, proj)
	if b.LifecycleState != bindings.StateError {
		t.Fatalf("update failure: %+v", b)
	}
	delete(fb.fail, "replace")
	b, err := l.Sync(ctx, b, proj)
	if err != nil || b.LifecycleState != bindings.StateActive || b.LastErrorCode != nil {
		t.Fatalf("sync from error: %+v %v", b, err)
	}

	fb.fail["drop"] = errors.New("busy")
	b, _ = l.Deprovision(ctx, b)
	if b.LifecycleState != bindings.StateDeprovisioning || b.LastErrorMessage == nil {
		t.Fatalf("failed drop must stay deprovisioning: %+v", b)
	}
	delete(fb.fail, "drop")
	b, _ = l.Deprovision(ctx, b) // retry, as the reconciler would
	if b.LifecycleState != bindings.StateDisabled {
		t.Fatalf("retried drop: %+v", b)
	}
	if _, err := l.Sync(ctx, b, proj); !errors.Is(err, bindings.ErrConcurrentChange) {
		t.Fatalf("sync of a disabled binding: %v", err)
	}
}

func TestObserve(t *testing.T) {
	st, fb, l := setup(bindings.StateActive)
	fb.observed = []bindings.Observed{
		{EngineCatalog: "sales", Present: true, Connector: "postgresql"},
		{EngineCatalog: "legacy", Present: true, Connector: "hive"},
		{EngineCatalog: "system", Present: true},
		{EngineCatalog: "baleia_test_1234", Present: true},
	}
	if _, err := l.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.observations["sales"] != "managed" || st.observations["legacy"] != "adopted" || len(st.observations) != 2 {
		t.Errorf("observations = %v", st.observations)
	}
	if !slices.Equal(st.adopted, []string{"legacy"}) || !slices.Equal(st.absentKeep, []string{"sales", "legacy"}) {
		t.Errorf("adopted %v, present %v", st.adopted, st.absentKeep)
	}
}
