package bindings_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// engineSim is a binder over a simulated engine: the catalogs it has and
// the version each was loaded with.
type engineSim struct {
	loaded map[string]string // name -> version
	ddl    []string
}

func (e *engineSim) EngineKind() string                      { return "fake" }
func (e *engineSim) Supports() map[connections.Kind][]string { return nil }
func (e *engineSim) ProtectedKeyPatterns() []string          { return nil }
func (e *engineSim) ReservedNames() []string                 { return []string{"system", "tpch"} }
func (e *engineSim) Project(bindings.BindingSpec, connections.Config) (bindings.Projection, error) {
	return bindings.Projection{}, nil
}
func (e *engineSim) Create(_ context.Context, _ bindings.EngineRef, p bindings.Projection) error {
	e.ddl = append(e.ddl, "create "+p.EngineCatalog)
	e.loaded[p.EngineCatalog] = p.Version
	return nil
}
func (e *engineSim) Replace(_ context.Context, _ bindings.EngineRef, p bindings.Projection) error {
	e.ddl = append(e.ddl, "replace "+p.EngineCatalog)
	e.loaded[p.EngineCatalog] = p.Version
	return nil
}
func (e *engineSim) Rename(context.Context, bindings.EngineRef, string, bindings.Projection) error {
	return nil
}
func (e *engineSim) Drop(_ context.Context, _ bindings.EngineRef, name string) error {
	e.ddl = append(e.ddl, "drop "+name)
	delete(e.loaded, name)
	return nil
}
func (e *engineSim) Confirm(_ context.Context, _ bindings.EngineRef, p bindings.Projection) (bindings.Confirmation, error) {
	v, ok := e.loaded[p.EngineCatalog]
	switch {
	case !ok:
		return bindings.Confirmation{Reason: "not_registered"}, nil
	case v != p.Version:
		return bindings.Confirmation{Reason: "version_mismatch"}, nil
	}
	return bindings.Confirmation{Confirmed: true}, nil
}
func (e *engineSim) Observe(context.Context, bindings.EngineRef) ([]bindings.Observed, error) {
	var out []bindings.Observed
	for name := range e.loaded {
		out = append(out, bindings.Observed{EngineCatalog: name, Present: true})
	}
	return out, nil
}

// manyStore keeps several managed bindings with the transition guards.
type manyStore struct {
	bindings    map[[16]byte]*db.CatalogBinding
	transitions int
	adopted     []string
	leftovers   []string
}

func (s *manyStore) TransitionBinding(_ context.Context, a db.TransitionBindingParams) (db.CatalogBinding, error) {
	b, ok := s.bindings[a.ID.Bytes]
	if !ok || !slices.Contains(a.FromStates, b.LifecycleState) {
		return db.CatalogBinding{}, pgx.ErrNoRows
	}
	s.transitions++
	b.LifecycleState, b.LastErrorCode = a.ToState, a.ErrorCode
	if a.DesiredVersion != nil {
		b.DesiredVersion = a.DesiredVersion
	}
	return *b, nil
}
func (s *manyStore) ListManagedBindingsOfEngine(context.Context, pgtype.UUID) ([]db.CatalogBinding, error) {
	var out []db.CatalogBinding
	for _, b := range s.bindings {
		out = append(out, *b)
	}
	slices.SortFunc(out, func(a, b db.CatalogBinding) int { return strings.Compare(a.EngineCatalogName, b.EngineCatalogName) })
	return out, nil
}
func (s *manyStore) ManagedBindingNamesOfEngine(context.Context, pgtype.UUID) ([]string, error) {
	var out []string
	for _, b := range s.bindings {
		out = append(out, b.EngineCatalogName)
	}
	return out, nil
}
func (s *manyStore) ObservedManagedNamesOfEngine(context.Context, pgtype.UUID) ([]string, error) {
	return s.leftovers, nil
}
func (s *manyStore) UpsertEngineCatalogObservation(context.Context, db.UpsertEngineCatalogObservationParams) error {
	return nil
}
func (s *manyStore) MarkEngineCatalogsAbsent(context.Context, db.MarkEngineCatalogsAbsentParams) error {
	return nil
}
func (s *manyStore) UpsertNativeCatalog(_ context.Context, a db.UpsertNativeCatalogParams) (db.Catalog, error) {
	return db.Catalog{Name: a.Name}, nil
}
func (s *manyStore) UpsertAdoptedBinding(_ context.Context, a db.UpsertAdoptedBindingParams) error {
	s.adopted = append(s.adopted, a.EngineCatalogName)
	return nil
}
func (s *manyStore) MarkEngineCatalogManaged(context.Context, db.MarkEngineCatalogManagedParams) error {
	return nil
}

func (s *manyStore) add(name, state string) {
	id := [16]byte{byte(len(s.bindings) + 1)}
	s.bindings[id] = &db.CatalogBinding{ID: pgtype.UUID{Bytes: id, Valid: true}, EngineCatalogName: name, Mode: "managed", LifecycleState: state, Enabled: true}
}

func (s *manyStore) state(name string) string {
	for _, b := range s.bindings {
		if b.EngineCatalogName == name {
			return b.LifecycleState
		}
	}
	return ""
}

func reconciler(st *manyStore, eng *engineSim) bindings.Reconciler {
	l := bindings.Lifecycle{Store: st, Binder: eng, ConnectionID: pgtype.UUID{Bytes: [16]byte{7}, Valid: true}}
	return bindings.Reconciler{
		Lifecycle: l, Store: st,
		Project: func(_ context.Context, b db.CatalogBinding) (bindings.Projection, error) {
			return bindings.Projection{EngineCatalog: b.EngineCatalogName, Version: "v2"}, nil
		},
	}
}

func newSim() (*manyStore, *engineSim) {
	return &manyStore{bindings: map[[16]byte]*db.CatalogBinding{}}, &engineSim{loaded: map[string]string{"system": "", "tpch": ""}}
}

// TestReconcile_AdoptedNeverDropped: a catalog the engine has without a
// managed binding is reported and recorded as adopted, never dropped.
func TestReconcile_AdoptedNeverDropped(t *testing.T) {
	st, eng := newSim()
	eng.loaded["customer_hive"] = "theirs"
	rep, err := reconciler(st, eng).Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Adopted, []string{"customer_hive"}) || !slices.Equal(st.adopted, []string{"customer_hive"}) {
		t.Fatalf("adopted = %v recorded %v", rep.Adopted, st.adopted)
	}
	for _, d := range eng.ddl {
		if strings.HasPrefix(d, "drop") {
			t.Fatalf("a catalog Baleia does not manage was dropped: %v", eng.ddl)
		}
	}
	if _, ok := eng.loaded["customer_hive"]; !ok || len(rep.Actions) != 0 {
		t.Fatalf("customer_hive must stay untouched: %v %+v", eng.loaded, rep.Actions)
	}
}

// TestReconcile_MissingManagedCreated: an active catalog missing in the
// engine is created again and ends active.
func TestReconcile_MissingManagedCreated(t *testing.T) {
	st, eng := newSim()
	st.add("sales", bindings.StateActive)
	rep, err := reconciler(st, eng).Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Actions) != 1 || rep.Actions[0].Kind != bindings.ActionCreate || rep.Actions[0].Result != bindings.ResultApplied {
		t.Fatalf("actions = %+v", rep.Actions)
	}
	if st.state("sales") != bindings.StateActive || eng.loaded["sales"] != "v2" {
		t.Fatalf("state %s, engine %v", st.state("sales"), eng.loaded)
	}
}

// TestReconcile_VersionMismatchRecreated: an active catalog loaded with an
// older version is applied again.
func TestReconcile_VersionMismatchRecreated(t *testing.T) {
	st, eng := newSim()
	st.add("sales", bindings.StateActive)
	eng.loaded["sales"] = "v1"
	rep, _ := reconciler(st, eng).Run(context.Background(), false)
	if len(rep.Actions) != 1 || rep.Actions[0].Kind != bindings.ActionRecreate || rep.Actions[0].Reason != "version_mismatch" || eng.loaded["sales"] != "v2" {
		t.Fatalf("actions = %+v engine %v", rep.Actions, eng.loaded)
	}
}

// TestReconcile_PendingDrop: a deprovisioning catalog still in the engine
// is dropped and ends disabled; one already gone just ends disabled.
func TestReconcile_PendingDrop(t *testing.T) {
	st, eng := newSim()
	st.add("old", bindings.StateDeprovisioning)
	st.add("gone", bindings.StateDeprovisioning)
	eng.loaded["old"] = "v1"
	rep, err := reconciler(st, eng).Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eng.loaded["old"]; ok || st.state("old") != bindings.StateDisabled || st.state("gone") != bindings.StateDisabled {
		t.Fatalf("engine %v states old=%s gone=%s", eng.loaded, st.state("old"), st.state("gone"))
	}
	kinds := map[string]string{}
	for _, a := range rep.Actions {
		kinds[a.Catalog] = a.Kind
	}
	if kinds["old"] != bindings.ActionDrop || kinds["gone"] != bindings.ActionNone {
		t.Fatalf("actions = %+v", rep.Actions)
	}
}

// TestReconcile_DryRunNoMutations: a dry run reports what it would do and
// changes nothing.
func TestReconcile_DryRunNoMutations(t *testing.T) {
	st, eng := newSim()
	st.add("sales", bindings.StateActive)
	st.add("old", bindings.StateDeprovisioning)
	eng.loaded["old"] = "v1"
	eng.loaded["customer_hive"] = "theirs"
	rep, err := reconciler(st, eng).Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(eng.ddl) != 0 || st.transitions != 0 || len(st.adopted) != 0 {
		t.Fatalf("a dry run changed state: ddl %v transitions %d adopted %v", eng.ddl, st.transitions, st.adopted)
	}
	if rep.Mutating() != 2 || !slices.Equal(rep.Adopted, []string{"customer_hive"}) {
		t.Fatalf("report = %+v", rep)
	}
	for _, a := range rep.Actions {
		if a.Result != bindings.ResultDryRun {
			t.Fatalf("action %+v", a)
		}
	}
}

// TestReconcile_MaxActions: past the limit the remaining actions are
// reported, not applied.
func TestReconcile_MaxActions(t *testing.T) {
	st, eng := newSim()
	for i := range 30 {
		st.add(fmt.Sprintf("c%02d", i), bindings.StateActive)
	}
	r := reconciler(st, eng)
	r.MaxActions = 20
	var audited int
	r.OnAction = func(context.Context, bindings.Action) { audited++ }
	rep, _ := r.Run(context.Background(), false)
	applied, reported := 0, 0
	for _, a := range rep.Actions {
		switch a.Result {
		case bindings.ResultApplied:
			applied++
		case bindings.ResultReported:
			reported++
		}
	}
	if applied != 20 || reported != 10 || audited != 20 {
		t.Fatalf("applied %d reported %d audited %d", applied, reported, audited)
	}
}

// TestReconcile_Idempotent: after a successful run the next one finds
// nothing to change.
func TestReconcile_Idempotent(t *testing.T) {
	st, eng := newSim()
	st.add("sales", bindings.StateActive)
	st.add("old", bindings.StateDeprovisioning)
	eng.loaded["old"] = "v1"
	r := reconciler(st, eng)
	if _, err := r.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	rep, _ := r.Run(context.Background(), false)
	if rep.Mutating() != 0 {
		t.Fatalf("second run = %+v", rep.Actions)
	}
}

// TestReconcile_LeftoverDropped: a name Baleia managed that no binding owns
// any more (a rename's old name) is dropped; it is not adopted.
func TestReconcile_LeftoverDropped(t *testing.T) {
	st, eng := newSim()
	st.leftovers = []string{"sales_old"}
	eng.loaded["sales_old"] = "v1"
	rep, _ := reconciler(st, eng).Run(context.Background(), false)
	if _, ok := eng.loaded["sales_old"]; ok || len(rep.Adopted) != 0 {
		t.Fatalf("engine %v report %+v", eng.loaded, rep)
	}
}
