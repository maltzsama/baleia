package activities

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/testsuite"
)

// listingProvider is a direct-engine provider whose reader lists catalogs.
type listingProvider struct {
	kind     connections.Kind
	catalogs []string
	err      error
}

func (p *listingProvider) Kind() connections.Kind     { return p.kind }
func (p *listingProvider) Label() string              { return string(p.kind) }
func (p *listingProvider) Family() connections.Family { return connections.FamilyDatabase }
func (p *listingProvider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleDirect}
}
func (p *listingProvider) ConfigSchema() []connections.Field              { return nil }
func (p *listingProvider) EngineKind() string                             { return string(p.kind) }
func (p *listingProvider) Ping(context.Context, connections.Config) error { return nil }
func (p *listingProvider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}
func (p *listingProvider) Validate(cfg connections.Config) (connections.Config, error) {
	return cfg, nil
}
func (p *listingProvider) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &listingReader{names: p.catalogs}, nil
}

type listingReader struct {
	connections.MetadataReader
	names []string
}

func (r *listingReader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	var out []connections.CatalogRef
	for _, n := range r.names {
		out = append(out, connections.CatalogRef{Name: n})
	}
	return out, nil
}
func (r *listingReader) Close() error { return nil }

// refreshQuerier serves the engines and records the native catalog writes.
type refreshQuerier struct {
	db.Querier
	engines  []db.ListEnabledEnginesWithConnectionRow
	natives  []string
	bindings []db.UpsertNativeBindingParams
	disabled []db.DisableMissingNativeBindingsParams
}

func (q *refreshQuerier) ListEnabledEnginesWithConnection(context.Context) ([]db.ListEnabledEnginesWithConnectionRow, error) {
	return q.engines, nil
}
func (q *refreshQuerier) UpsertNativeCatalog(_ context.Context, p db.UpsertNativeCatalogParams) (db.Catalog, error) {
	q.natives = append(q.natives, p.Name)
	return db.Catalog{ID: pgtype.UUID{Bytes: [16]byte{byte(len(q.natives))}, Valid: true}, Name: p.Name}, nil
}
func (q *refreshQuerier) UpsertNativeBinding(_ context.Context, p db.UpsertNativeBindingParams) (db.CatalogBinding, error) {
	q.bindings = append(q.bindings, p)
	return db.CatalogBinding{}, nil
}
func (q *refreshQuerier) DisableMissingNativeBindings(_ context.Context, p db.DisableMissingNativeBindingsParams) (int64, error) {
	q.disabled = append(q.disabled, p)
	return 0, nil
}

func engineRow(id byte, class, kind, connName string) db.ListEnabledEnginesWithConnectionRow {
	return db.ListEnabledEnginesWithConnectionRow{
		Engine:            db.Engine{ID: pgtype.UUID{Bytes: [16]byte{id}, Valid: true}, Name: connName, Class: class, Kind: kind, Enabled: true},
		CatalogConnection: db.CatalogConnection{ID: pgtype.UUID{Bytes: [16]byte{0xC0, id}, Valid: true}, Name: connName, Type: kind, Config: []byte("{}")},
	}
}

// TestRefreshEngineCatalogs: a direct engine's native catalogs are upserted
// and the missing ones disabled; a federated engine is observed; an
// unreachable engine is counted and the others still refresh.
func TestRefreshEngineCatalogs(t *testing.T) {
	reg, err := connections.NewRegistry(
		&listingProvider{kind: connections.KindClickHouse, catalogs: []string{"default", "logs"}},
		&listingProvider{kind: connections.KindSQLServer, err: errors.New("unreachable")},
	)
	if err != nil {
		t.Fatal(err)
	}
	trinoRow := engineRow(3, "federated", "trino", "trino")
	trinoRow.ClusterID = pgtype.UUID{Bytes: [16]byte{0xCC}, Valid: true}
	q := &refreshQuerier{engines: []db.ListEnabledEnginesWithConnectionRow{
		engineRow(1, "direct", "sqlserver", "mssql"),
		engineRow(2, "direct", "clickhouse", "ch"),
		trinoRow,
	}}
	var observed []pgtype.UUID
	a := &Activities{queries: q, connections: reg}
	a.SetEngineObserver(func(_ context.Context, eng db.Engine, cluster pgtype.UUID) error {
		observed = append(observed, eng.ID, cluster)
		return nil
	})
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = a
	ts := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	ts.RegisterActivity(RefreshEngineCatalogs)
	v, err := ts.ExecuteActivity(RefreshEngineCatalogs)
	if err != nil {
		t.Fatal(err)
	}
	var res RefreshEngineCatalogsResult
	if err := v.Get(&res); err != nil {
		t.Fatal(err)
	}
	if res.Engines != 3 || res.Failed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !slices.Equal(q.natives, []string{"default", "logs"}) || len(q.bindings) != 2 || q.bindings[1].EngineCatalogName != "logs" {
		t.Fatalf("natives %v bindings %+v", q.natives, q.bindings)
	}
	if len(q.disabled) != 1 || !slices.Equal(q.disabled[0].Present, []string{"default", "logs"}) || q.disabled[0].EngineID.Bytes[0] != 2 {
		t.Fatalf("disabled = %+v", q.disabled)
	}
	if len(observed) != 2 || observed[0].Bytes[0] != 3 || observed[1].Bytes[0] != 0xCC {
		t.Fatalf("observed = %v", observed)
	}
}
