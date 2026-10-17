package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// probeEngine accepts any statement and returns an empty result set; it records
// the SQL so tests can assert what validation ran.
type probeEngine struct {
	readOnly queryengine.ReadOnlySupport
	failWith error
	lastSQL  string
}

func (e *probeEngine) ID() string                                            { return "semantic-engine" }
func (e *probeEngine) Kind() string                                          { return "trino" }
func (e *probeEngine) Class() queryengine.Class                              { return queryengine.ClassFederated }
func (e *probeEngine) Dialect() queryengine.Dialect                          { return dialect.MustGet(dialect.Trino) }
func (e *probeEngine) Cancel(context.Context, queryengine.QueryHandle) error { return nil }
func (e *probeEngine) Ping(context.Context) error                            { return nil }
func (e *probeEngine) Introspector() queryengine.Introspector                { return nil }
func (e *probeEngine) Close() error                                          { return nil }

func (e *probeEngine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{Dialect: dialect.Trino, ReadOnly: e.readOnly}
}

func (e *probeEngine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	if spec.ReadOnly && e.readOnly != queryengine.ReadOnlyEnforced {
		return nil, queryengine.NewError(queryengine.CodeReadOnly, "trino", "", "read-only unsupported")
	}
	return &probeSession{e: e}, nil
}

type probeSession struct{ e *probeEngine }

func (s *probeSession) Execute(_ context.Context, sql string, _ queryengine.Callbacks, sink queryengine.RecordSink) error {
	s.e.lastSQL = sql
	if s.e.failWith != nil {
		return s.e.failWith
	}
	if err := sink.Begin(arrow.NewSchema(nil, nil)); err != nil {
		return err
	}
	return sink.End(queryengine.Stats{})
}

func (s *probeSession) Close() error { return nil }

func metricFixture(t *testing.T) (*metaFixture, *probeEngine) {
	t.Helper()
	f := newMetaFixture(t)
	e := &probeEngine{readOnly: queryengine.ReadOnlyEnforced}
	f.h.cfg = &config.Config{TrinoUserIdentity: "uuid"}
	f.h.engines = fixedEngines{e}
	return f, e
}

func simpleDefinition(aggregation, column string) map[string]interface{} {
	def := map[string]interface{}{"type": "simple", "aggregation": aggregation}
	if column != "" {
		def["column"] = column
	}
	return def
}

func createMetric(t *testing.T, f *metaFixture, ctx context.Context, body SemanticMetricCreate) CreateSemanticMetricResponseObject {
	t.Helper()
	r, err := f.h.CreateSemanticMetric(ctx, CreateSemanticMetricRequestObject{Id: f.ws, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSemanticMetrics_CreateValidateListDelete(t *testing.T) {
	f, engine := metricFixture(t)
	ctx := f.as(f.owner)

	r := createMetric(t, f, ctx, SemanticMetricCreate{
		Slug: "net_revenue", Label: "Net revenue", CatalogId: f.lake,
		Namespace: []string{"sales"}, Table: "orders",
		Definition: simpleDefinition("sum", "net_revenue"),
	})
	metric, ok := r.(CreateSemanticMetric201JSONResponse)
	if !ok {
		t.Fatalf("create: %T", r)
	}
	if metric.Slug != "net_revenue" || metric.FormatVersion != 1 {
		t.Fatalf("metric = %+v", metric)
	}
	if !strings.Contains(engine.lastSQL, `SUM("net_revenue")`) || !strings.Contains(engine.lastSQL, "LIMIT 0") {
		t.Fatalf("validation probe = %s", engine.lastSQL)
	}

	list, err := f.h.ListSemanticMetrics(ctx, ListSemanticMetricsRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if got := list.(ListSemanticMetrics200JSONResponse); len(got) != 1 || got[0].Slug != "net_revenue" {
		t.Fatalf("list = %+v", got)
	}

	got, err := f.h.GetSemanticMetric(ctx, GetSemanticMetricRequestObject{Id: f.ws, MetricId: metric.Id})
	if err != nil {
		t.Fatal(err)
	}
	if got.(GetSemanticMetric200JSONResponse).Label != "Net revenue" {
		t.Fatalf("get = %+v", got)
	}

	if d, err := f.h.DeleteSemanticMetric(ctx, DeleteSemanticMetricRequestObject{Id: f.ws, MetricId: metric.Id}); err != nil {
		t.Fatal(err)
	} else if _, ok := d.(DeleteSemanticMetric204Response); !ok {
		t.Fatalf("delete: %T", d)
	}
	if g, err := f.h.GetSemanticMetric(ctx, GetSemanticMetricRequestObject{Id: f.ws, MetricId: metric.Id}); err != nil {
		t.Fatal(err)
	} else if _, ok := g.(GetSemanticMetric404JSONResponse); !ok {
		t.Fatalf("after delete: %T", g)
	}
}

func TestSemanticMetrics_InvalidDefinition422(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)

	unknown := simpleDefinition("sum", "net_revenue")
	unknown["surprise"] = true
	r := createMetric(t, f, ctx, SemanticMetricCreate{
		Slug: "bad", Label: "Bad", CatalogId: f.lake,
		Namespace: []string{"sales"}, Table: "orders", Definition: unknown,
	})
	if _, ok := r.(CreateSemanticMetric422JSONResponse); !ok {
		t.Fatalf("unknown field: %T", r)
	}

	noColumn := simpleDefinition("sum", "")
	r = createMetric(t, f, ctx, SemanticMetricCreate{
		Slug: "bad2", Label: "Bad", CatalogId: f.lake,
		Namespace: []string{"sales"}, Table: "orders", Definition: noColumn,
	})
	if _, ok := r.(CreateSemanticMetric422JSONResponse); !ok {
		t.Fatalf("missing column: %T", r)
	}
}

func TestSemanticMetrics_EngineRejection422(t *testing.T) {
	f, engine := metricFixture(t)
	ctx := f.as(f.owner)
	engine.failWith = queryengine.NewError(queryengine.CodeNotFound, "trino", "42S02", "missing column")

	r := createMetric(t, f, ctx, SemanticMetricCreate{
		Slug: "broken", Label: "Broken", CatalogId: f.lake,
		Namespace: []string{"sales"}, Table: "orders",
		Definition: simpleDefinition("sum", "nonexistent"),
	})
	resp, ok := r.(CreateSemanticMetric422JSONResponse)
	if !ok {
		t.Fatalf("engine rejection: %T", r)
	}
	if resp.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", resp.Code)
	}
}

func TestSemanticMetrics_CustomSQLRequiresReadOnly422(t *testing.T) {
	f, engine := metricFixture(t)
	ctx := f.as(f.owner)
	engine.readOnly = queryengine.ReadOnlyNone

	def := map[string]interface{}{"type": "simple", "custom_sql": `sum("net_revenue")`}
	r := createMetric(t, f, ctx, SemanticMetricCreate{
		Slug: "custom", Label: "Custom", CatalogId: f.lake,
		Namespace: []string{"sales"}, Table: "orders", Definition: def,
	})
	resp, ok := r.(CreateSemanticMetric422JSONResponse)
	if !ok {
		t.Fatalf("custom_sql without read-only: %T", r)
	}
	if resp.Code != "custom_sql_requires_read_only" {
		t.Fatalf("code = %q", resp.Code)
	}
}

func TestSemanticMetrics_SlugConflict409(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	body := SemanticMetricCreate{
		Slug: "dup", Label: "Dup", CatalogId: f.lake,
		Namespace: []string{"sales"}, Table: "orders", Definition: simpleDefinition("count", ""),
	}
	if r := createMetric(t, f, ctx, body); r == nil {
		t.Fatal("first create failed")
	}
	if r, ok := createMetric(t, f, ctx, body).(CreateSemanticMetric409JSONResponse); !ok {
		t.Fatalf("second create: %T", r)
	}
}

func TestSemanticMetrics_UngrantedCatalog404(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	r := createMetric(t, f, ctx, SemanticMetricCreate{
		Slug: "secret", Label: "Secret", CatalogId: f.other,
		Namespace: []string{"sales"}, Table: "orders_secret", Definition: simpleDefinition("count", ""),
	})
	if _, ok := r.(CreateSemanticMetric404JSONResponse); !ok {
		t.Fatalf("ungranted catalog: %T", r)
	}
}

func TestSemanticMetrics_SQL(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	def := simpleDefinition("sum", "net_revenue")
	def["time_column"] = "order_date"
	def["time_grains"] = []interface{}{"day", "month"}
	r := createMetric(t, f, ctx, SemanticMetricCreate{
		Slug: "net_revenue", Label: "Net revenue", CatalogId: f.lake,
		Namespace: []string{"sales"}, Table: "orders", Definition: def,
	})
	metric := r.(CreateSemanticMetric201JSONResponse)

	grain := Month
	from, to := mustTime(t, "2026-01-01T00:00:00Z"), mustTime(t, "2026-02-01T00:00:00Z")
	sql, err := f.h.GetSemanticMetricSql(ctx, GetSemanticMetricSqlRequestObject{
		Id: f.ws, MetricId: metric.Id,
		Params: GetSemanticMetricSqlParams{Grain: &grain, From: &from, To: &to},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := sql.(GetSemanticMetricSql200JSONResponse)
	if !ok {
		t.Fatalf("sql: %T", sql)
	}
	if !out.Portable || !strings.Contains(out.Sql, `date_trunc('month', "order_date")`) || !strings.Contains(out.Sql, `AS "net_revenue"`) {
		t.Fatalf("sql = %+v", out)
	}

	week := Week
	if r, err := f.h.GetSemanticMetricSql(ctx, GetSemanticMetricSqlRequestObject{
		Id: f.ws, MetricId: metric.Id, Params: GetSemanticMetricSqlParams{Grain: &week},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(GetSemanticMetricSql422JSONResponse); !ok {
		t.Fatalf("disallowed grain: %T", r)
	}

	badFrom, badTo := mustTime(t, "2026-02-01T00:00:00Z"), mustTime(t, "2026-01-01T00:00:00Z")
	if r, err := f.h.GetSemanticMetricSql(ctx, GetSemanticMetricSqlRequestObject{
		Id: f.ws, MetricId: metric.Id, Params: GetSemanticMetricSqlParams{From: &badFrom, To: &badTo},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(GetSemanticMetricSql422JSONResponse); !ok {
		t.Fatalf("inverted range: %T", r)
	}
}

func TestMetadataTable_Doc(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	upsertDoc(t, f, ctx, f.lake, nil, SemanticDocUpsert{Description: "Orders table.", Tags: tags("certified")})
	col := "id"
	upsertDoc(t, f, ctx, f.lake, &col, SemanticDocUpsert{Description: "Order id."})

	r, err := f.h.GetMetadataTable(ctx, GetMetadataTableRequestObject{Id: f.ws, TableId: f.orders})
	if err != nil {
		t.Fatal(err)
	}
	detail, ok := r.(GetMetadataTable200JSONResponse)
	if !ok {
		t.Fatalf("table: %T", r)
	}
	if detail.Doc == nil || detail.Doc.Description != "Orders table." {
		t.Fatalf("table doc = %+v", detail.Doc)
	}
	if len(detail.Columns) == 0 || detail.Columns[0].Name != "id" || detail.Columns[0].Doc == nil || detail.Columns[0].Doc.Description != "Order id." {
		t.Fatalf("column docs = %+v", detail.Columns)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
