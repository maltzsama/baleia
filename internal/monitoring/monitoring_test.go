package monitoring

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	// Policies is not used by these tests (see policies_test.go).
	Policies
	bindings  []db.CatalogBinding
	queries   db.ListTrinoQuerySnapshotsByEngineParams
	snapshot  error
	failed    []db.UpdateMaintenanceRunParams
	clusterTo pgtype.Timestamptz
}

func (f *fakeStore) ListEngines(context.Context) ([]db.Engine, error) { return nil, nil }

func (f *fakeStore) GetLatestClusterSnapshotByEngine(context.Context, pgtype.UUID) (db.ClusterSnapshot, error) {
	return db.ClusterSnapshot{}, pgx.ErrNoRows
}

func (f *fakeStore) ListClusterSnapshotsByEngine(_ context.Context, arg db.ListClusterSnapshotsByEngineParams) ([]db.ClusterSnapshot, error) {
	f.clusterTo = arg.FromTime
	return nil, nil
}

func (f *fakeStore) ListTrinoQuerySnapshotsByEngine(_ context.Context, arg db.ListTrinoQuerySnapshotsByEngineParams) ([]db.TrinoQuerySnapshot, error) {
	f.queries = arg
	return nil, nil
}

func (f *fakeStore) GetTrinoQuerySnapshotByEngine(context.Context, db.GetTrinoQuerySnapshotByEngineParams) (db.TrinoQuerySnapshot, error) {
	return db.TrinoQuerySnapshot{}, f.snapshot
}

func (f *fakeStore) GetCatalogByID(context.Context, pgtype.UUID) (db.Catalog, error) {
	return db.Catalog{}, pgx.ErrNoRows
}

func (f *fakeStore) ListTableHealthByCatalog(context.Context, db.ListTableHealthByCatalogParams) ([]db.TableHealthSnapshot, error) {
	return nil, nil
}

func (f *fakeStore) ListBindingsByCatalog(context.Context, pgtype.UUID) ([]db.CatalogBinding, error) {
	return f.bindings, nil
}

func (f *fakeStore) ListMaintenanceRuns(context.Context, db.ListMaintenanceRunsParams) ([]db.MaintenanceRun, error) {
	return nil, nil
}

func (f *fakeStore) CreateMaintenanceRun(_ context.Context, arg db.CreateMaintenanceRunParams) (db.MaintenanceRun, error) {
	return db.MaintenanceRun{ID: newID(), BindingID: arg.BindingID, Retention: arg.Retention}, nil
}

func (f *fakeStore) UpdateMaintenanceRun(_ context.Context, arg db.UpdateMaintenanceRunParams) (db.MaintenanceRun, error) {
	f.failed = append(f.failed, arg)
	return db.MaintenanceRun{}, nil
}

func TestQueries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &fakeStore{}
	s := New(st)
	if _, err := s.Queries(ctx, newID(), QueryFilter{}); err != nil || st.queries.Lim != DefaultQueries || st.queries.UserName != nil {
		t.Errorf("defaults = %+v, %v", st.queries, err)
	}
	huge := math.MaxInt32 + 10
	_, _ = s.Queries(ctx, newID(), QueryFilter{Offset: &huge})
	if st.queries.Off != math.MaxInt32 {
		t.Errorf("offset = %d; an out-of-range offset saturates instead of wrapping", st.queries.Off)
	}

	st.snapshot = pgx.ErrNoRows
	if _, err := s.Query(ctx, newID(), "q"); !errors.Is(err, ErrQueryNotFound) {
		t.Errorf("unknown query = %v", err)
	}
	st.snapshot = errors.New("connection reset")
	if _, err := s.Query(ctx, newID(), "q"); !errors.Is(err, st.snapshot) {
		t.Errorf("failed lookup = %v, want it returned", err)
	}
	if _, err := s.Catalog(ctx, newID()); !errors.Is(err, ErrCatalogNotFound) {
		t.Errorf("unknown catalog = %v", err)
	}
}

func TestPrepare(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	capableEngine := newID()
	st := &fakeStore{bindings: []db.CatalogBinding{
		{ID: newID(), EngineID: newID(), Enabled: true},
		{ID: newID(), EngineID: capableEngine, Enabled: true},
		{ID: newID(), EngineID: capableEngine},
	}}
	s := New(st)
	capable := func(_ context.Context, id pgtype.UUID) bool { return id == capableEngine }
	week := " 7d "

	var inv *InvalidError
	for name, m := range map[string]Maintenance{
		"no operations":     {},
		"unknown operation": {Operations: []string{"vacuum"}},
		"no retention":      {Operations: []string{"expire_snapshots"}},
	} {
		if _, err := s.Prepare(ctx, m, capable); !errors.As(err, &inv) {
			t.Errorf("%s = %v, want an InvalidError", name, err)
		}
	}
	plan, err := s.Prepare(ctx, Maintenance{Operations: []string{"expire_snapshots"}, Retention: &week}, capable)
	if err != nil || plan.Binding.ID != st.bindings[1].ID || plan.Retention != "7d" {
		t.Fatalf("Prepare = %+v, %v; want the capable binding and a trimmed retention", plan, err)
	}
	if _, err := s.Prepare(ctx, Maintenance{Operations: []string{"optimize"}}, func(context.Context, pgtype.UUID) bool { return false }); !errors.Is(err, ErrNoCapableBinding) {
		t.Errorf("no capable binding = %v", err)
	}

	explicit := st.bindings[1].ID
	if plan, err := s.Prepare(ctx, Maintenance{Operations: []string{"optimize"}, Binding: &explicit}, capable); err != nil || plan.Binding.EngineID != capableEngine {
		t.Errorf("explicit capable binding = %+v, %v", plan.Binding, err)
	}
	for name, id := range map[string]pgtype.UUID{
		"binding of another catalog": newID(),
		"disabled binding":           st.bindings[2].ID,
	} {
		if _, err := s.Prepare(ctx, Maintenance{Operations: []string{"optimize"}, Binding: &id}, capable); !errors.As(err, &inv) {
			t.Errorf("%s = %v, want an InvalidError", name, err)
		}
	}
	incapable := st.bindings[0].ID
	if _, err := s.Prepare(ctx, Maintenance{Operations: []string{"optimize"}, Binding: &incapable}, capable); !errors.Is(err, ErrNoCapableBinding) {
		t.Errorf("explicit incapable binding = %v, want ErrNoCapableBinding", err)
	}

	run, err := s.Record(ctx, Plan{Maintenance: Maintenance{Operations: []string{"optimize"}}, Binding: plan.Binding}, "maintenance-1")
	if err != nil || run.Retention != nil {
		t.Errorf("Record = %+v, %v; an empty retention is stored as NULL", run, err)
	}
	s.Fail(ctx, run.ID, errors.New("temporal down"))
	if len(st.failed) != 1 || st.failed[0].Status != "failed" || *st.failed[0].ErrorCode != "workflow_start_failed" {
		t.Errorf("Fail = %+v", st.failed)
	}
}
