package manage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

func newID() db.UUID { return db.UUID{Bytes: uuid.New(), Valid: true} }

// fakeStore keeps engines and native catalogs; tx commits only on success.
type fakeStore struct {
	engines  map[[16]byte]db.Engine
	catalogs map[string]db.UUID
	blockers db.CountEngineBlockersRow
	// createErr fails CreateEngine, bindErr UpsertNativeBinding, deleteErr
	// DeleteEngine.
	createErr, bindErr, deleteErr error
	admin                         bool
	runsLimit                     int32
}

func newStore() *fakeStore {
	return &fakeStore{engines: map[[16]byte]db.Engine{}, catalogs: map[string]db.UUID{}}
}

func (f *fakeStore) ListEngines(context.Context) ([]db.Engine, error) {
	f.admin = true
	return nil, nil
}

func (f *fakeStore) ListEnginesVisibleToUser(context.Context, db.UUID) ([]db.Engine, error) {
	f.admin = false
	return nil, nil
}

func (f *fakeStore) ListBindingsByEngine(context.Context, db.UUID) ([]db.CatalogBinding, error) {
	return nil, nil
}

func (f *fakeStore) ListBindingsByCatalog(context.Context, db.UUID) ([]db.CatalogBinding, error) {
	return nil, nil
}

func (f *fakeStore) CountEngineBlockers(context.Context, db.UUID) (db.CountEngineBlockersRow, error) {
	return f.blockers, nil
}

func (f *fakeStore) UpdateEngine(_ context.Context, arg db.UpdateEngineParams) (db.Engine, error) {
	e := f.engines[arg.ID.Bytes]
	if arg.Enabled != nil {
		e.Enabled = *arg.Enabled
	}
	return e, nil
}

func (f *fakeStore) CreateEngine(_ context.Context, arg db.CreateEngineParams) (db.Engine, error) {
	if f.createErr != nil {
		return db.Engine{}, f.createErr
	}
	e := db.Engine{ID: newID(), Name: arg.Name, Kind: arg.Kind, Class: arg.Class, ConnectionID: arg.ConnectionID}
	f.engines[e.ID.Bytes] = e
	return e, nil
}

func (f *fakeStore) UpsertNativeCatalog(_ context.Context, arg db.UpsertNativeCatalogParams) (db.Catalog, error) {
	id := newID()
	f.catalogs[arg.Name] = id
	return db.Catalog{ID: id, Name: arg.Name}, nil
}

func (f *fakeStore) UpsertNativeBinding(context.Context, db.UpsertNativeBindingParams) (db.CatalogBinding, error) {
	return db.CatalogBinding{}, f.bindErr
}

func (f *fakeStore) DeleteNativeCatalogsOfEngine(context.Context, db.UUID) error {
	f.catalogs = map[string]db.UUID{}
	return nil
}

func (f *fakeStore) DeleteEngine(_ context.Context, id db.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.engines, id.Bytes)
	return nil
}

func (f *fakeStore) GetEngineByID(_ context.Context, id db.UUID) (db.Engine, error) {
	if e, ok := f.engines[id.Bytes]; ok {
		return e, nil
	}
	return db.Engine{}, db.ErrNoRows
}

func (f *fakeStore) ListEngineObservations(context.Context, db.UUID) ([]db.EngineCatalogObservation, error) {
	return nil, nil
}

func (f *fakeStore) CountEngineImpact(context.Context, db.UUID) (db.CountEngineImpactRow, error) {
	return db.CountEngineImpactRow{}, nil
}

func (f *fakeStore) GetReconcileRun(context.Context, db.GetReconcileRunParams) (db.CatalogReconcileRun, error) {
	return db.CatalogReconcileRun{}, db.ErrNoRows
}

func (f *fakeStore) ListReconcileRuns(_ context.Context, arg db.ListReconcileRunsParams) ([]db.CatalogReconcileRun, error) {
	f.runsLimit = arg.Limit
	return nil, nil
}

func (f *fakeStore) tx(_ context.Context, fn func(TxStore) error) error {
	engines, catalogs := map[[16]byte]db.Engine{}, map[string]db.UUID{}
	for k, v := range f.engines {
		engines[k] = v
	}
	for k, v := range f.catalogs {
		catalogs[k] = v
	}
	if err := fn(f); err != nil {
		f.engines, f.catalogs = engines, catalogs
		return err
	}
	return nil
}

func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)

	if _, _, err := s.Create(ctx, NewEngine{Name: "Bad Name"}); !errors.Is(err, ErrInvalidName) {
		t.Errorf("bad name = %v, want ErrInvalidName", err)
	}
	e, natives, err := s.Create(ctx, NewEngine{Name: "pg", Kind: "postgresql", ConnectionID: newID(), Natives: []string{"app", "analytics"}})
	if err != nil || e.Class != "direct" || len(natives) != 2 {
		t.Fatalf("Create = %+v, %v, %v; want a direct engine and its two native catalogs", e, natives, err)
	}

	st.createErr = &db.PGError{Code: "23505", ConstraintName: "engines_connection_id_key"}
	if _, _, err := s.Create(ctx, NewEngine{Name: "pg2"}); !errors.Is(err, ErrConnectionTaken) {
		t.Errorf("connection taken = %v", err)
	}
	st.createErr = &db.PGError{Code: "23505", ConstraintName: "engines_name_key"}
	if _, _, err := s.Create(ctx, NewEngine{Name: "pg"}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("name taken = %v", err)
	}
}

// TestCreate_Atomic: a failed binding leaves neither the engine nor its
// catalogs behind.
func TestCreate_Atomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	st.bindErr = errors.New("connection reset")
	if _, _, err := s.Create(ctx, NewEngine{Name: "pg", Natives: []string{"app"}}); !errors.Is(err, st.bindErr) {
		t.Fatalf("Create = %v, want the binding error", err)
	}
	if len(st.engines) != 0 || len(st.catalogs) != 0 {
		t.Errorf("left %d engines and %d catalogs behind", len(st.engines), len(st.catalogs))
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	e, _, _ := s.Create(ctx, NewEngine{Name: "pg", Natives: []string{"app"}})

	var inUse *InUseError
	st.blockers = db.CountEngineBlockersRow{ManagedBindings: 1}
	if err := s.Delete(ctx, e.ID); !errors.As(err, &inUse) {
		t.Errorf("with a managed binding = %v, want InUseError", err)
	}
	st.blockers = db.CountEngineBlockersRow{}
	st.deleteErr = &db.PGError{Code: "23001"}
	if err := s.Delete(ctx, e.ID); !errors.As(err, &inUse) {
		t.Errorf("still referenced = %v, want InUseError", err)
	}
	if len(st.catalogs) != 1 {
		t.Error("a refused delete keeps the native catalogs")
	}
	st.deleteErr = nil
	if err := s.Delete(ctx, e.ID); err != nil || len(st.engines) != 0 {
		t.Errorf("Delete = %v, %d engines left", err, len(st.engines))
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	_, _ = s.List(ctx, newID(), true)
	if !st.admin {
		t.Error("an admin lists every engine")
	}
	_, _ = s.List(ctx, newID(), false)
	if st.admin {
		t.Error("a member lists the engines visible to them")
	}
}

func TestReconcileRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	if _, err := s.ReconcileRuns(ctx, newID(), 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown engine = %v, want ErrNotFound", err)
	}
	e, _, _ := s.Create(ctx, NewEngine{Name: "pg"})
	if _, err := s.ReconcileRuns(ctx, e.ID, 1000); err != nil || st.runsLimit != DefaultRuns {
		t.Errorf("out-of-range limit = %d, %v; want DefaultRuns", st.runsLimit, err)
	}
	if _, err := s.ReconcileRun(ctx, e.ID, newID()); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("unknown run = %v, want ErrRunNotFound", err)
	}
}

func TestNormalizeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already valid", "sales_db", "sales_db"},
		{"upper case and spaces", "Sales DB", "sales_db"},
		{"runs of invalid characters collapse", "a !! b", "a_b"},
		{"edges are trimmed", "  --prod--  ", "prod"},
		{"leading digit gets a prefix", "2024 sales", "e_2024_sales"},
		{"nothing usable", "!!!", ""},
		{"empty", "", ""},
		{"cut to the column limit", strings.Repeat("a", 80), strings.Repeat("a", 62)},
		{"prefix stays within the limit", "1" + strings.Repeat("a", 80), "e_1" + strings.Repeat("a", 60)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeName(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if got != "" {
				if err := CheckName(got); err != nil {
					t.Fatalf("NormalizeName(%q) = %q is refused by CheckName: %v", tt.in, got, err)
				}
			}
		})
	}
}

// CreateIn writes through the store it is given and opens no transaction of
// its own, so the caller's transaction owns the commit.
func TestCreateIn_UsesTheCallersStore(t *testing.T) {
	t.Parallel()
	f := newStore()
	conn := newID()
	eng, natives, err := CreateIn(context.Background(), f, NewEngine{Name: "pg", Kind: "postgresql", ConnectionID: conn, Natives: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if eng.Name != "pg" || len(natives) != 2 || len(f.engines) != 1 {
		t.Fatalf("engine %q natives %d engines %d", eng.Name, len(natives), len(f.engines))
	}
}

func TestCreateError(t *testing.T) {
	t.Parallel()
	plain := errors.New("boom")
	if got := CreateError(plain); !errors.Is(got, plain) {
		t.Fatalf("an unrelated error changed: %v", got)
	}
	if CreateError(nil) != nil {
		t.Fatal("nil became an error")
	}
}
