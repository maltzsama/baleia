package models

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

// orders is a valid table model.
var orders = map[string]any{
	"source":      map[string]any{"kind": "table", "namespace": []string{"sales"}, "table": "orders"},
	"primary_key": []string{"id"},
}

type fakeStore struct {
	models    map[[16]byte]db.SemanticModel
	slugTaken bool
}

func newStore() *fakeStore { return &fakeStore{models: map[[16]byte]db.SemanticModel{}} }

func (f *fakeStore) ListVisibleSemanticModels(context.Context, db.ListVisibleSemanticModelsParams) ([]db.SemanticModel, error) {
	var out []db.SemanticModel
	for _, m := range f.models {
		out = append(out, m)
	}
	return out, nil
}

func (f *fakeStore) GetSemanticModel(_ context.Context, arg db.GetSemanticModelParams) (db.SemanticModel, error) {
	if m, ok := f.models[arg.ID.Bytes]; ok && m.WorkspaceID == arg.WorkspaceID {
		return m, nil
	}
	return db.SemanticModel{}, pgx.ErrNoRows
}

func (f *fakeStore) SetSemanticModelCertification(_ context.Context, arg db.SetSemanticModelCertificationParams) (db.SemanticModel, error) {
	m := f.models[arg.ID.Bytes]
	m.Certified, m.CertifiedAt = arg.Certified, pgtype.Timestamptz{Valid: arg.Certified}
	f.models[arg.ID.Bytes] = m
	return m, nil
}

func (f *fakeStore) CreateSemanticModel(_ context.Context, _ pgtype.UUID, in db.CreateSemanticModelParams) (db.SemanticModel, error) {
	if f.slugTaken {
		return db.SemanticModel{}, &pgconn.PgError{Code: "23505"}
	}
	m := db.SemanticModel{ID: newID(), WorkspaceID: in.WorkspaceID, Slug: in.Slug, Label: in.Label, Certified: in.Certified, CatalogID: in.CatalogID, Definition: in.Definition}
	f.models[m.ID.Bytes] = m
	return m, nil
}

func (f *fakeStore) UpdateSemanticModel(_ context.Context, _ pgtype.UUID, in db.UpdateSemanticModelParams, _ *int32) (db.SemanticModel, bool, error) {
	m := f.models[in.ID.Bytes]
	m.Slug, m.Label, m.Certified, m.Definition = in.Slug, in.Label, in.Certified, in.Definition
	f.models[in.ID.Bytes] = m
	return m, true, nil
}

func (f *fakeStore) DeleteSemanticModel(context.Context, pgtype.UUID, pgtype.UUID, pgtype.UUID) error {
	return nil
}

func TestPrepareNewAndCreate(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st, st)
	f := Fields{Slug: "orders", Label: "Orders", CatalogID: newID(), Certified: true}

	p, err := s.PrepareNew(ctx, newID(), newID(), f, orders)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, newID(), newID(), newID(), p, false); !errors.Is(err, ErrCertifyForbidden) {
		t.Errorf("certify without the right = %v", err)
	}
	m, err := s.Create(ctx, newID(), newID(), newID(), p, true)
	if err != nil || !m.Certified || !m.CertifiedAt.Valid {
		t.Fatalf("Create = %+v, %v", m, err)
	}

	var def *InvalidDefinitionError
	if _, err := s.PrepareNew(ctx, newID(), newID(), f, map[string]any{"source": map[string]any{"kind": "table"}, "primary_key": []string{"id"}}); !errors.As(err, &def) {
		t.Errorf("table source without a table = %v", err)
	}
	if _, err := s.PrepareNew(ctx, newID(), newID(), f, map[string]any{"unknown": 1}); !errors.As(err, &def) {
		t.Errorf("unknown field = %v", err)
	}
	var field *InvalidFieldError
	if _, err := s.PrepareNew(ctx, newID(), newID(), Fields{Slug: "Bad", Label: "x"}, orders); !errors.As(err, &field) {
		t.Errorf("bad slug = %v", err)
	}
	st.slugTaken = true
	if _, err := s.Create(ctx, newID(), newID(), newID(), p, true); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("duplicate slug = %v", err)
	}
}

func TestMergeFields(t *testing.T) {
	current := db.SemanticModel{Slug: "orders", Label: "Orders", Certified: true}
	no := false
	if _, err := MergeFields(current, Change{Certified: &no}, false); !errors.Is(err, ErrCertifyForbidden) {
		t.Errorf("uncertify as non-manager = %v", err)
	}
	label := "All orders"
	if f, _ := MergeFields(current, Change{Label: &label}, false); !f.Certified || f.Label != label {
		t.Errorf("label change = %+v", f)
	}
	if f, _ := MergeFields(current, Change{ChangesDefinition: true}, false); f.Certified {
		t.Error("a definition change by a non-manager uncertifies")
	}
	binding := newID()
	if f, _ := MergeFields(current, Change{BindingID: &binding}, false); f.Certified {
		t.Error("a binding change by a non-manager uncertifies")
	}
}

func TestPrepareUpdate_KeepsTheStoredDefinition(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st, st)
	raw, _ := json.Marshal(orders)
	current := db.SemanticModel{ID: newID(), Slug: "orders", Label: "Orders", CatalogID: newID(), Definition: raw}
	st.models[current.ID.Bytes] = current

	f, _ := MergeFields(current, Change{}, true)
	p, err := s.PrepareUpdate(ctx, newID(), newID(), current, f, nil)
	if err != nil || string(p.Raw) != string(raw) {
		t.Fatalf("PrepareUpdate = %s, %v; want the stored definition", p.Raw, err)
	}
}
