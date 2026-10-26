package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	bindings []db.CatalogBinding
	engines  []db.Engine
	countErr error
	disabled bool
}

func (f *fakeStore) CountNamespacesByCatalog(context.Context, pgtype.UUID) (int64, error) {
	return 3, f.countErr
}

func (f *fakeStore) CountTablesByCatalog(context.Context, pgtype.UUID) (int64, error) {
	return 7, nil
}

func (f *fakeStore) CountCatalogImpact(context.Context, pgtype.UUID) (db.CountCatalogImpactRow, error) {
	return db.CountCatalogImpactRow{}, nil
}

func (f *fakeStore) CountConnectionImpact(context.Context, pgtype.UUID) (db.CountConnectionImpactRow, error) {
	return db.CountConnectionImpactRow{Catalogs: 2}, nil
}

func (f *fakeStore) ListBindingsByCatalog(context.Context, pgtype.UUID) ([]db.CatalogBinding, error) {
	return f.bindings, nil
}

func (f *fakeStore) ListEngines(context.Context) ([]db.Engine, error) { return f.engines, nil }

func (f *fakeStore) UpdateCatalogMeta(_ context.Context, arg db.UpdateCatalogMetaParams) (db.Catalog, error) {
	return db.Catalog{ID: arg.ID, Name: arg.Name, Description: arg.Description}, nil
}

func (f *fakeStore) SetCatalogEnabled(context.Context, db.SetCatalogEnabledParams) error {
	f.disabled = true
	return nil
}

func (f *fakeStore) DisableManagedBindingsOfCatalog(context.Context, pgtype.UUID) error { return nil }

func (f *fakeStore) tx(_ context.Context, fn func(TxStore) error) error { return fn(f) }

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{}
	s := New(st, st.tx)
	desc := "orders"
	cat := db.Catalog{ID: newID(), Name: "sales", Description: &desc}

	blank := "  "
	if _, err := s.Update(ctx, cat, Edit{Name: &blank}); !errors.Is(err, ErrEmptyName) {
		t.Errorf("blank name = %v, want ErrEmptyName", err)
	}
	name := " revenue "
	got, err := s.Update(ctx, cat, Edit{Name: &name})
	if err != nil || got.Name != "revenue" || got.Description != &desc {
		t.Errorf("rename = %+v, %v; want the trimmed name and the description kept", got, err)
	}
}

func TestRetire(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{bindings: []db.CatalogBinding{{Mode: "managed", LifecycleState: "active"}, {Mode: "native"}}}
	s := New(st, st.tx)

	if _, err := s.Retire(ctx, db.Catalog{Origin: "native"}); !errors.Is(err, ErrNotManaged) {
		t.Errorf("native catalog = %v, want ErrNotManaged", err)
	}
	managed, err := s.Retire(ctx, db.Catalog{ID: newID(), Origin: "source"})
	if err != nil || len(managed) != 1 || !st.disabled {
		t.Fatalf("Retire = %d bindings, %v, disabled %v; want the managed binding back", len(managed), err, st.disabled)
	}

	st.disabled = false
	st.bindings = append(st.bindings, db.CatalogBinding{Mode: "managed", LifecycleState: "updating"})
	if _, err := s.Retire(ctx, db.Catalog{Origin: "source"}); !errors.Is(err, ErrBindingBusy) || st.disabled {
		t.Errorf("busy binding = %v, disabled %v; want ErrBindingBusy before any write", err, st.disabled)
	}
}

func TestSizeAndImpact(t *testing.T) {
	ctx := context.Background()
	conn := newID()
	st := &fakeStore{countErr: errors.New("timeout"), engines: []db.Engine{{Name: "other", ConnectionID: newID()}, {Name: "pg", ConnectionID: conn}}}
	s := New(st, st.tx)

	size := s.Size(ctx, newID())
	if size.Namespaces != nil || size.Tables == nil || *size.Tables != 7 {
		t.Errorf("size = %+v; a failed count is left out", size)
	}
	impact, err := s.ConnectionImpact(ctx, conn)
	if err != nil || impact.Catalogs != 2 || impact.Engine == nil || impact.Engine.Name != "pg" {
		t.Errorf("ConnectionImpact = %+v, %v; want the engine on the connection", impact, err)
	}
}
