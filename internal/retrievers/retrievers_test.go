package retrievers

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	retrievers map[[16]byte]db.Retriever
	dests      []db.ListRetrieverDestinationsRow
}

func (f *fakeStore) ListRetrieversByWorkspace(context.Context, pgtype.UUID) ([]db.Retriever, error) {
	return nil, nil
}

func (f *fakeStore) GetRetrieverByID(_ context.Context, arg db.GetRetrieverByIDParams) (db.Retriever, error) {
	if r, ok := f.retrievers[arg.ID.Bytes]; ok && r.WorkspaceID == arg.WorkspaceID {
		return r, nil
	}
	return db.Retriever{}, pgx.ErrNoRows
}

func (f *fakeStore) CreateRetriever(_ context.Context, arg db.CreateRetrieverParams) (db.Retriever, error) {
	r := db.Retriever{ID: newID(), WorkspaceID: arg.WorkspaceID, Name: arg.Name, DefinitionSql: arg.DefinitionSql, DestCatalog: arg.DestCatalog, DestNamespace: arg.DestNamespace, DestTable: arg.DestTable, DestBindingID: arg.DestBindingID, CronSchedule: arg.CronSchedule, RefreshType: arg.RefreshType, Status: "active", NextRefreshAt: arg.NextRefreshAt}
	f.retrievers[r.ID.Bytes] = r
	return r, nil
}

func (f *fakeStore) UpdateRetriever(_ context.Context, arg db.UpdateRetrieverParams) (db.Retriever, error) {
	r := f.retrievers[arg.ID.Bytes]
	r.Name, r.DefinitionSql, r.DestCatalog, r.DestBindingID, r.CronSchedule, r.Status, r.ErrorCode = arg.Name, arg.DefinitionSql, arg.DestCatalog, arg.DestBindingID, arg.CronSchedule, arg.Status, arg.ErrorCode
	f.retrievers[arg.ID.Bytes] = r
	return r, nil
}

func (f *fakeStore) DeleteRetriever(_ context.Context, arg db.DeleteRetrieverParams) (int64, error) {
	if _, ok := f.retrievers[arg.ID.Bytes]; !ok {
		return 0, nil
	}
	delete(f.retrievers, arg.ID.Bytes)
	return 1, nil
}

func (f *fakeStore) ListRetrieverRefreshHistory(context.Context, db.ListRetrieverRefreshHistoryParams) ([]db.RetrieverRefreshHistory, error) {
	return nil, nil
}

func (f *fakeStore) LatestRetrieverRefresh(context.Context, pgtype.UUID) (db.LatestRetrieverRefreshRow, error) {
	return db.LatestRetrieverRefreshRow{}, pgx.ErrNoRows
}

func (f *fakeStore) ListRetrieverDestinations(context.Context, pgtype.UUID) ([]db.ListRetrieverDestinationsRow, error) {
	return f.dests, nil
}

func (f *fakeStore) GetBinding(context.Context, pgtype.UUID) (db.CatalogBinding, error) {
	return db.CatalogBinding{}, pgx.ErrNoRows
}
func (f *fakeStore) GetCatalogByID(context.Context, pgtype.UUID) (db.Catalog, error) {
	return db.Catalog{}, pgx.ErrNoRows
}
func (f *fakeStore) GetEngineByID(context.Context, pgtype.UUID) (db.Engine, error) {
	return db.Engine{}, pgx.ErrNoRows
}

type fakeWorkflows struct{ args [][]any }

func (w *fakeWorkflows) ExecuteWorkflow(_ context.Context, _ client.StartWorkflowOptions, _ any, args ...any) (client.WorkflowRun, error) {
	w.args = append(w.args, args)
	return nil, nil
}

type fixture struct {
	s    *Service
	st   *fakeStore
	wf   *fakeWorkflows
	ws   pgtype.UUID
	dest db.ListRetrieverDestinationsRow
}

func setup(t *testing.T) fixture {
	t.Helper()
	trino, ok := dialect.NewRegistry().Get(dialect.Trino)
	if !ok {
		t.Fatal("no trino dialect")
	}
	dest := db.ListRetrieverDestinationsRow{BindingID: newID(), EngineCatalogName: "lake", CatalogName: "Lake"}
	st := &fakeStore{retrievers: map[[16]byte]db.Retriever{}, dests: []db.ListRetrieverDestinationsRow{dest}}
	wf := &fakeWorkflows{}
	return fixture{s: New(st, wf, nil, trino), st: st, wf: wf, ws: newID(), dest: dest}
}

func (f fixture) draft() Draft {
	return Draft{Name: "daily", DefinitionSQL: "select 1", DestBindingID: &f.dest.BindingID, DestNamespace: "mart", DestTable: "daily", CronSchedule: "0 3 * * *"}
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	r, err := f.s.Create(ctx, f.ws, newID(), f.draft())
	if err != nil || r.DestCatalog != "lake" || r.RefreshType != FullRefresh || !r.NextRefreshAt.Valid {
		t.Fatalf("Create = %+v, %v", r, err)
	}
	legacy := f.draft()
	legacy.DestBindingID, legacy.DestCatalog = nil, "Lake"
	if r, err := f.s.Create(ctx, f.ws, newID(), legacy); err != nil || r.DestBindingID != f.dest.BindingID {
		t.Errorf("legacy catalog name = %+v, %v", r, err)
	}

	for _, c := range []struct {
		name string
		edit func(*Draft)
		// invalid expects an *InvalidError; otherwise want.
		invalid bool
		want    error
	}{
		{name: "two statements", edit: func(d *Draft) { d.DefinitionSQL = "select 1; select 2" }, invalid: true},
		{name: "not a select", edit: func(d *Draft) { d.DefinitionSQL = "delete from t" }, invalid: true},
		{name: "bad table", edit: func(d *Draft) { d.DestTable = "bad-name" }, invalid: true},
		{name: "incremental", edit: func(d *Draft) { d.RefreshType = "incremental" }, want: ErrRefreshNotSupported},
		{name: "foreign binding", edit: func(d *Draft) { other := newID(); d.DestBindingID = &other }, want: ErrDestinationNotEligible},
		{name: "every minute", edit: func(d *Draft) { d.CronSchedule = "* * * * *" }, invalid: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := f.draft()
			c.edit(&d)
			_, err := f.s.Create(ctx, f.ws, newID(), d)
			var inv *InvalidError
			if c.invalid {
				if !errors.As(err, &inv) {
					t.Errorf("err = %v, want an InvalidError", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestUpdate_ClearsADestinationError(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	r, _ := f.s.Create(ctx, f.ws, newID(), f.draft())
	r.Status, r.ErrorCode = "error", new(string)
	f.st.retrievers[r.ID.Bytes] = r

	name := "renamed"
	got, err := f.s.Update(ctx, f.ws, r.ID, Change{Name: &name})
	if err != nil || got.Name != name || got.Status != "active" || got.ErrorCode != nil {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	if _, err := f.s.Update(ctx, newID(), r.ID, Change{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign workspace = %v, want ErrNotFound", err)
	}
}

func TestRefreshAndDelete(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	r, _ := f.s.Create(ctx, f.ws, newID(), f.draft())
	if err := f.s.Refresh(ctx, f.ws, r.ID, ""); err != nil {
		t.Fatal(err)
	}
	if len(f.wf.args) != 1 || f.wf.args[0][1] != FullRefresh {
		t.Errorf("workflow args = %v", f.wf.args)
	}
	if err := f.s.Refresh(ctx, f.ws, r.ID, "incremental"); !errors.Is(err, ErrRefreshNotSupported) {
		t.Errorf("incremental refresh = %v", err)
	}
	if err := f.s.Delete(ctx, f.ws, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Delete(ctx, f.ws, r.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

// TestHistory_ScopedToTheWorkspace: a retriever of another workspace has no
// history to show, even when its id is known.
func TestHistory_ScopedToTheWorkspace(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	r, _ := f.s.Create(ctx, f.ws, newID(), f.draft())
	if _, err := f.s.History(ctx, f.ws, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.History(ctx, newID(), r.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("History from another workspace = %v, want ErrNotFound", err)
	}
}

// TestValidateDefinition_AnyWhitespaceAfterTheKeyword: multi-line SQL from
// the editor breaks the line right after SELECT or WITH.
func TestValidateDefinition_AnyWhitespaceAfterTheKeyword(t *testing.T) {
	f := setup(t)
	for _, def := range []string{"SELECT\n  a\nFROM t", "with\tx AS (select 1) select * from x", "select 1", "  Select 1"} {
		if err := f.s.ValidateDefinition(def); err != nil {
			t.Errorf("ValidateDefinition(%q) = %v", def, err)
		}
	}
	for _, def := range []string{"selected_view", "insert into t select 1", "withdraw"} {
		if err := f.s.ValidateDefinition(def); err == nil {
			t.Errorf("ValidateDefinition(%q) accepted", def)
		}
	}
}
