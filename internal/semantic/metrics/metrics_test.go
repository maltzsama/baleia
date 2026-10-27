package metrics

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

var count = map[string]any{"type": "simple", "aggregation": "count"}

// fakeStore is the read side and, as Content, the versioned write side.
type fakeStore struct {
	metrics   map[[16]byte]db.SemanticMetric
	slugTaken bool
	certified []bool
}

func newStore() *fakeStore { return &fakeStore{metrics: map[[16]byte]db.SemanticMetric{}} }

func (f *fakeStore) ListVisibleSemanticMetrics(context.Context, db.ListVisibleSemanticMetricsParams) ([]db.SemanticMetric, error) {
	var out []db.SemanticMetric
	for _, m := range f.metrics {
		out = append(out, m)
	}
	return out, nil
}

func (f *fakeStore) ListSemanticMetricVersions(context.Context, pgtype.UUID) ([]db.ListSemanticMetricVersionsRow, error) {
	return nil, nil
}

func (f *fakeStore) GetSemanticMetric(_ context.Context, arg db.GetSemanticMetricParams) (db.SemanticMetric, error) {
	if m, ok := f.metrics[arg.ID.Bytes]; ok && m.WorkspaceID == arg.WorkspaceID {
		return m, nil
	}
	return db.SemanticMetric{}, pgx.ErrNoRows
}

func (f *fakeStore) SetSemanticMetricCertification(_ context.Context, arg db.SetSemanticMetricCertificationParams) (db.SemanticMetric, error) {
	f.certified = append(f.certified, arg.Certified)
	m := f.metrics[arg.ID.Bytes]
	m.Certified = arg.Certified
	m.CertifiedAt = pgtype.Timestamptz{Valid: arg.Certified}
	f.metrics[arg.ID.Bytes] = m
	return m, nil
}

func (f *fakeStore) CreateSemanticMetric(_ context.Context, _ pgtype.UUID, in db.CreateSemanticMetricParams) (db.SemanticMetric, error) {
	if f.slugTaken {
		return db.SemanticMetric{}, &pgconn.PgError{Code: "23505"}
	}
	m := db.SemanticMetric{ID: newID(), WorkspaceID: in.WorkspaceID, SpaceID: in.SpaceID, Slug: in.Slug, Label: in.Label, Description: in.Description, Certified: in.Certified, CatalogID: in.CatalogID, TableName: in.TableName, Definition: in.Definition}
	f.metrics[m.ID.Bytes] = m
	return m, nil
}

func (f *fakeStore) UpdateSemanticMetric(_ context.Context, _ pgtype.UUID, in db.UpdateSemanticMetricParams, _ *int32) (db.SemanticMetric, bool, error) {
	m := f.metrics[in.ID.Bytes]
	m.Slug, m.Label, m.Description, m.Certified, m.TableName, m.Definition = in.Slug, in.Label, in.Description, in.Certified, in.TableName, in.Definition
	f.metrics[in.ID.Bytes] = m
	return m, true, nil
}

func (f *fakeStore) DeleteSemanticMetric(context.Context, pgtype.UUID, pgtype.UUID, pgtype.UUID) error {
	return nil
}

func fields() Fields {
	return Fields{Slug: "orders", Label: "Orders", CatalogID: newID(), Table: "orders"}
}

func TestPrepare(t *testing.T) {
	t.Parallel()
	if _, err := Prepare(fields(), count); err != nil {
		t.Fatal(err)
	}
	var def *InvalidDefinitionError
	if _, err := Prepare(fields(), map[string]any{"type": "simple", "unknown": 1}); !errors.As(err, &def) {
		t.Errorf("unknown field = %v, want an InvalidDefinitionError", err)
	}
	var field *InvalidFieldError
	for _, f := range []Fields{
		{Slug: "Bad Slug", Label: "x", Table: "t"},
		{Slug: "ok", Label: " ", Table: "t"},
		{Slug: "ok", Label: "x", Table: ""},
	} {
		if _, err := Prepare(f, count); !errors.As(err, &field) {
			t.Errorf("Prepare(%+v) = %v, want an InvalidFieldError", f, err)
		}
	}
}

func TestCreate_Certification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st)
	f := fields()
	f.Certified = true
	p, _ := Prepare(f, count)

	if _, err := s.Create(ctx, newID(), newID(), newID(), p, false); !errors.Is(err, ErrCertifyForbidden) {
		t.Fatalf("certify without the right = %v", err)
	}
	m, err := s.Create(ctx, newID(), newID(), newID(), p, true)
	if err != nil || !m.Certified || !m.CertifiedAt.Valid {
		t.Fatalf("Create = %+v, %v; want certified with its timestamp", m, err)
	}
	st.slugTaken = true
	if _, err := s.Create(ctx, newID(), newID(), newID(), p, true); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("duplicate slug = %v, want ErrSlugTaken", err)
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()
	p, _ := Prepare(fields(), count)
	current := db.SemanticMetric{Slug: p.Slug, Label: p.Label, TableName: p.Table, CatalogID: p.CatalogID, Certified: true, Definition: p.Raw}

	yes, no := true, false
	if _, err := Merge(current, Change{Certified: &no}, false); !errors.Is(err, ErrCertifyForbidden) {
		t.Errorf("uncertify as non-manager = %v", err)
	}
	// A definition change by a non-manager uncertifies; a label change does not.
	label := "Order count"
	if m, err := Merge(current, Change{Label: &label}, false); err != nil || !m.Certified || m.Label != label {
		t.Errorf("label change = %+v, %v", m, err)
	}
	table := "orders_v2"
	if m, _ := Merge(current, Change{Table: &table}, false); m.Certified {
		t.Error("a definition change by a non-manager uncertifies")
	}
	if m, _ := Merge(current, Change{Table: &table}, true); !m.Certified {
		t.Error("a manager keeps the certification")
	}
	if m, _ := Merge(current, Change{Certified: &yes}, true); !m.Certified || string(m.Raw) != string(current.Definition) {
		t.Error("an unchanged definition is kept as stored")
	}
	bad := map[string]any{"type": "nope"}
	var def *InvalidDefinitionError
	if _, err := Merge(current, Change{Definition: &bad}, true); !errors.As(err, &def) {
		t.Errorf("bad definition = %v", err)
	}
}

func TestList_Filters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st)
	space := newID()
	st.metrics[[16]byte{1}] = db.SemanticMetric{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, SpaceID: space, Label: "Revenue", Slug: "revenue", Certified: true}
	st.metrics[[16]byte{2}] = db.SemanticMetric{ID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, SpaceID: newID(), Label: "Orders", Slug: "orders"}

	yes := true
	for _, c := range []struct {
		name string
		f    Filter
		want int
	}{
		{"all", Filter{}, 2},
		{"space", Filter{SpaceID: &space}, 1},
		{"certified", Filter{Certified: &yes}, 1},
		{"q on label", Filter{Q: " REVEN "}, 1},
		{"limit", Filter{Limit: 1}, 1},
	} {
		items, err := s.List(ctx, newID(), newID(), c.f)
		if err != nil || len(items) != c.want {
			t.Errorf("%s: %d items, %v; want %d", c.name, len(items), err, c.want)
		}
	}
}
