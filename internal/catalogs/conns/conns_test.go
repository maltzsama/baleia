package conns

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	conns             []db.CatalogConnection
	engines           []db.Engine
	createErr, delErr error
	updateErr         error
	listedEngines     bool
}

func (f *fakeStore) ListVisibleCatalogConnections(context.Context, db.ListVisibleCatalogConnectionsParams) ([]db.CatalogConnection, error) {
	return f.conns, nil
}

func (f *fakeStore) ListEngines(context.Context) ([]db.Engine, error) {
	f.listedEngines = true
	return f.engines, nil
}

func (f *fakeStore) CreateCatalogConnection(_ context.Context, arg db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	return db.CatalogConnection{ID: arg.ID, Name: arg.Name}, f.createErr
}

func (f *fakeStore) UpdateCatalogConnection(_ context.Context, arg db.UpdateCatalogConnectionParams) (db.CatalogConnection, error) {
	return db.CatalogConnection{ID: arg.ID}, f.updateErr
}

func (f *fakeStore) DeleteCatalogConnection(context.Context, pgtype.UUID) error { return f.delErr }

func TestList_WithoutEngine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	free, backing := db.CatalogConnection{ID: newID()}, db.CatalogConnection{ID: newID()}
	st := &fakeStore{conns: []db.CatalogConnection{free, backing}, engines: []db.Engine{{ConnectionID: backing.ID}}}
	s := New(st)

	all, err := s.List(ctx, newID(), false, Filter{})
	if err != nil || len(all) != 2 || st.listedEngines {
		t.Fatalf("List = %d, %v; want both without reading engines", len(all), err)
	}
	got, err := s.List(ctx, newID(), false, Filter{WithoutEngine: true})
	if err != nil || len(got) != 1 || got[0].ID != free.ID {
		t.Errorf("without engine = %+v, %v; want only the free connection", got, err)
	}
}

func TestWrites_MapConflicts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &fakeStore{createErr: &pgconn.PgError{Code: "23505"}}
	s := New(st)
	if _, err := s.Create(ctx, db.CreateCatalogConnectionParams{Name: "pg"}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("duplicate name = %v, want ErrNameTaken", err)
	}
	st.updateErr = &pgconn.PgError{Code: "23505"}
	taken := "pg"
	if _, err := s.Update(ctx, db.UpdateCatalogConnectionParams{ID: newID(), Name: &taken}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("rename to a taken name = %v, want ErrNameTaken", err)
	}

	st.delErr = &pgconn.PgError{Code: "23001", ConstraintName: "engines_connection_id_fkey"}
	if err := s.Delete(ctx, newID()); !errors.Is(err, ErrBacksEngine) {
		t.Errorf("engine on it = %v, want ErrBacksEngine", err)
	}
	other := &pgconn.PgError{Code: "23503", ConstraintName: "something_else_fkey"}
	st.delErr = other
	if err := s.Delete(ctx, newID()); !errors.Is(err, other) {
		t.Errorf("other FK = %v, want it unchanged", err)
	}
}
