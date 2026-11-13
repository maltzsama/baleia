package conns

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

func newID() db.UUID { return db.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	conns             []db.CatalogConnection
	createErr, delErr error
	updateErr         error
}

func (f *fakeStore) ListVisibleCatalogConnections(context.Context, db.ListVisibleCatalogConnectionsParams) ([]db.CatalogConnection, error) {
	return f.conns, nil
}

func (f *fakeStore) CreateCatalogConnection(_ context.Context, arg db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	return db.CatalogConnection{ID: arg.ID, Name: arg.Name}, f.createErr
}

func (f *fakeStore) UpdateCatalogConnection(_ context.Context, arg db.UpdateCatalogConnectionParams) (db.CatalogConnection, error) {
	return db.CatalogConnection{ID: arg.ID}, f.updateErr
}

func (f *fakeStore) DeleteCatalogConnection(context.Context, db.UUID) error { return f.delErr }

func TestWrites_MapConflicts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &fakeStore{createErr: &db.PGError{Code: "23505"}}
	s := New(st)
	if _, err := s.Create(ctx, db.CreateCatalogConnectionParams{Name: "pg"}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("duplicate name = %v, want ErrNameTaken", err)
	}
	st.updateErr = &db.PGError{Code: "23505"}
	taken := "pg"
	if _, err := s.Update(ctx, db.UpdateCatalogConnectionParams{ID: newID(), Name: &taken}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("rename to a taken name = %v, want ErrNameTaken", err)
	}

	st.delErr = &db.PGError{Code: "23001", ConstraintName: "engines_connection_id_fkey"}
	if err := s.Delete(ctx, newID()); !errors.Is(err, ErrBacksEngine) {
		t.Errorf("engine on it = %v, want ErrBacksEngine", err)
	}
	other := &db.PGError{Code: "23503", ConstraintName: "something_else_fkey"}
	st.delErr = other
	if err := s.Delete(ctx, newID()); !errors.Is(err, other) {
		t.Errorf("other FK = %v, want it unchanged", err)
	}
}
