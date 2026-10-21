package api

import (
	"context"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// The definition a client sends carries neither the slug nor the binding, and
// a rename leaves the stored slug stale; the explore compiler must still find
// the model by the row's slug (#121).
func TestSemanticModels_CompileResolvesModelByRowSlug(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)

	definition := map[string]interface{}{
		"source":      map[string]interface{}{"kind": "table", "namespace": []string{"sales"}, "table": "orders"},
		"primary_key": []string{"id"},
		"dimensions": []map[string]interface{}{
			{"name": "id", "type": "int64"},
			{"name": "status", "type": "string"},
		},
		"metrics": []map[string]interface{}{{"name": "order_count", "aggregation": "count"}},
	}
	created, err := f.h.CreateSemanticModel(ctx, CreateSemanticModelRequestObject{
		Id: f.ws,
		Body: &SemanticModelCreate{
			Slug: "orders", Label: "Orders", CatalogId: f.lake, Definition: definition,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, ok := created.(CreateSemanticModel201JSONResponse)
	if !ok {
		t.Fatalf("create: %T", created)
	}

	compile := func(slug string) CompileExploreQueryResponseObject {
		t.Helper()
		metrics := []string{"order_count"}
		dimensions := []string{"status"}
		r, err := f.h.CompileExploreQuery(ctx, CompileExploreQueryRequestObject{
			Id: f.ws,
			Body: &ExploreCompileRequest{
				Query: ExploreQuery{Model: slug, Dimensions: &dimensions, Metrics: &metrics},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	out, ok := compile("orders").(CompileExploreQuery200JSONResponse)
	if !ok {
		t.Fatalf("compile by the created slug did not succeed")
	}
	if !strings.Contains(out.Sql, "orders") || !strings.Contains(out.Sql, "COUNT") {
		t.Fatalf("sql = %s", out.Sql)
	}

	// Renaming the model must not strand the explore query on the old slug.
	renamed := "orders_v2"
	if _, err := f.h.UpdateSemanticModel(ctx, UpdateSemanticModelRequestObject{
		Id: f.ws, ModelId: model.Id, Body: &SemanticModelUpdate{Slug: &renamed},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := compile(renamed).(CompileExploreQuery200JSONResponse); !ok {
		t.Fatalf("compile by the renamed slug did not succeed")
	}
	if _, ok := compile("orders").(CompileExploreQuery404JSONResponse); !ok {
		t.Fatalf("the old slug should no longer resolve")
	}
}

func migrate(t *testing.T, f *metaFixture, ctx context.Context, body MigrateV1Request) MigrateV1Report {
	t.Helper()
	r, err := f.h.MigrateSemanticMetricsV1(ctx, MigrateSemanticMetricsV1RequestObject{Id: f.ws, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	report, ok := r.(MigrateSemanticMetricsV1200JSONResponse)
	if !ok {
		t.Fatalf("migrate: %T", r)
	}
	return MigrateV1Report(report)
}

func listModels(t *testing.T, f *metaFixture, ctx context.Context) []SemanticModel {
	t.Helper()
	r, err := f.h.ListSemanticModels(ctx, ListSemanticModelsRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	return r.(ListSemanticModels200JSONResponse)
}

// v1 metrics of one table become one model whose metrics keep their slugs; a
// dry run writes nothing and a second run changes nothing (#91, #121).
func TestSemanticModels_MigrateV1(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	for _, body := range []SemanticMetricCreate{
		{Slug: "order_count", Label: "Orders", CatalogId: f.lake, Namespace: []string{"sales"}, Table: "orders", Definition: simpleDefinition("count", "")},
		{Slug: "notes", Label: "Notes", CatalogId: f.lake, Namespace: []string{"sales"}, Table: "orders", Definition: simpleDefinition("count_distinct", "order_note")},
	} {
		if _, ok := createMetric(t, f, ctx, body).(CreateSemanticMetric201JSONResponse); !ok {
			t.Fatalf("create metric %s", body.Slug)
		}
	}

	dry := migrate(t, f, ctx, MigrateV1Request{})
	if !dry.DryRun || len(dry.Items) != 1 {
		t.Fatalf("dry run = %+v", dry)
	}
	item := dry.Items[0]
	if item.Action != MigrateV1ItemActionCREATE || item.ModelSlug != "orders" || item.ModelId != nil {
		t.Fatalf("dry item = %+v", item)
	}
	if strings.Join(item.PrimaryKey, ",") != "id" || strings.Join(item.Metrics, ",") != "notes,order_count" {
		t.Fatalf("dry item = %+v", item)
	}
	if strings.Join(item.Columns, ",") != "id,order_note" {
		t.Fatalf("columns = %v (the list column has no portable type)", item.Columns)
	}
	if got := listModels(t, f, ctx); len(got) != 0 {
		t.Fatalf("a dry run wrote %d models", len(got))
	}

	apply := false
	applied := migrate(t, f, ctx, MigrateV1Request{DryRun: &apply})
	if applied.Items[0].Action != MigrateV1ItemActionCREATE || applied.Items[0].ModelId == nil {
		t.Fatalf("apply = %+v", applied.Items[0])
	}
	models := listModels(t, f, ctx)
	if len(models) != 1 || models[0].Slug != "orders" || models[0].Version != 1 {
		t.Fatalf("models = %+v", models)
	}

	// The migrated metrics compile with their v1 slugs.
	metrics := []string{"order_count", "notes"}
	r, err := f.h.CompileExploreQuery(ctx, CompileExploreQueryRequestObject{
		Id: f.ws, Body: &ExploreCompileRequest{Query: ExploreQuery{Model: "orders", Metrics: &metrics}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := r.(CompileExploreQuery200JSONResponse)
	if !ok {
		t.Fatalf("compile: %+v", r)
	}
	if !strings.Contains(out.Sql, "COUNT(DISTINCT") {
		t.Fatalf("sql = %s", out.Sql)
	}

	// Each migrated metric records its v1 source; the v1 page redirects by it.
	def := models[0].Definition["metrics"].([]interface{})
	for _, raw := range def {
		mt := raw.(map[string]interface{})
		if mt["migrated_from"] == nil || mt["migrated_from"] == "" {
			t.Fatalf("metric %v has no migrated_from", mt["name"])
		}
	}

	again := migrate(t, f, ctx, MigrateV1Request{DryRun: &apply})
	if again.Items[0].Action != MigrateV1ItemActionNOCHANGES {
		t.Fatalf("second run = %+v", again.Items[0])
	}

	// A new v1 metric on the same table is added to the existing model.
	createMetric(t, f, ctx, SemanticMetricCreate{Slug: "max_id", Label: "Max id", CatalogId: f.lake, Namespace: []string{"sales"}, Table: "orders", Definition: simpleDefinition("max", "id")})
	updated := migrate(t, f, ctx, MigrateV1Request{DryRun: &apply})
	if updated.Items[0].Action != MigrateV1ItemActionUPDATE {
		t.Fatalf("update run = %+v", updated.Items[0])
	}
	if got := listModels(t, f, ctx); len(got) != 1 || got[0].Version != 2 {
		t.Fatalf("models after update = %+v", got)
	}
}

// A table without an id column needs an explicit primary key.
func TestSemanticModels_MigrateV1NeedsPrimaryKey(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	if _, err := f.pool.Exec(context.Background(), `UPDATE metadata_columns SET name = 'order_id' WHERE table_id = $1 AND name = 'id'`, f.orders); err != nil {
		t.Fatal(err)
	}
	createMetric(t, f, ctx, SemanticMetricCreate{Slug: "order_count", Label: "Orders", CatalogId: f.lake, Namespace: []string{"sales"}, Table: "orders", Definition: simpleDefinition("count", "")})

	report := migrate(t, f, ctx, MigrateV1Request{})
	if report.Items[0].Action != MigrateV1ItemActionERROR || report.Items[0].ErrorCode == nil ||
		*report.Items[0].ErrorCode != MigrateV1ItemErrorCodePrimaryKeyRequired {
		t.Fatalf("item = %+v", report.Items[0])
	}
	apply := false
	chosen := migrate(t, f, ctx, MigrateV1Request{DryRun: &apply, Tables: &[]MigrateV1Table{{
		CatalogId: f.lake, Namespace: []string{"sales"}, Table: "orders", PrimaryKey: []string{"order_id"},
	}}})
	if chosen.Items[0].Action != MigrateV1ItemActionCREATE {
		t.Fatalf("with a primary key = %+v", chosen.Items[0])
	}
}

// A space that holds only models is not empty: deleting it without force must
// not cascade over them, and the content search finds models (#113, #121).
func TestSemanticModels_SpaceCountsAndSearch(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	created, err := f.h.CreateSemanticModel(ctx, CreateSemanticModelRequestObject{Id: f.ws, Body: &SemanticModelCreate{
		Slug: "orders", Label: "Orders model", CatalogId: f.lake,
		Definition: map[string]interface{}{
			"source":      map[string]interface{}{"kind": "table", "namespace": []string{"sales"}, "table": "orders"},
			"primary_key": []string{"id"},
			"dimensions":  []map[string]interface{}{{"name": "id", "type": "int64"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	model := created.(CreateSemanticModel201JSONResponse)
	n, err := f.h.queries.CountSpaceContent(context.Background(), pgtype.UUID{Bytes: *model.SpaceId, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("a space with a model counts as empty")
	}

	r, err := f.h.SearchContent(ctx, SearchContentRequestObject{Id: f.ws, Params: SearchContentParams{Q: "orders mod"}})
	if err != nil {
		t.Fatal(err)
	}
	items := r.(SearchContent200JSONResponse)
	if len(items) != 1 || items[0].Kind != "semantic_model" || items[0].Id != model.Id {
		t.Fatalf("search = %+v", items)
	}
}

// A valid query the compiler cannot express answers not_supported, not
// invalid_definition and never a 500.
func TestSemanticModels_CompileUnsupportedCode(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	create := func(slug string, def map[string]interface{}) {
		t.Helper()
		r, err := f.h.CreateSemanticModel(ctx, CreateSemanticModelRequestObject{Id: f.ws, Body: &SemanticModelCreate{
			Slug: slug, Label: slug, CatalogId: f.lake, Definition: def,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := r.(CreateSemanticModel201JSONResponse); !ok {
			t.Fatalf("create %s: %+v", slug, r)
		}
	}
	create("customers", map[string]interface{}{
		"source":      map[string]interface{}{"kind": "table", "namespace": []string{"sales"}, "table": "customers"},
		"primary_key": []string{"cid"},
		"dimensions":  []map[string]interface{}{{"name": "cid", "type": "int64"}},
		"metrics": []map[string]interface{}{
			{"name": "a", "aggregation": "count"},
			{"name": "b", "aggregation": "count"},
			{"name": "ratio", "derived": map[string]interface{}{"left": "a", "op": "/", "right": "b"}},
		},
	})
	create("orders", map[string]interface{}{
		"source":      map[string]interface{}{"kind": "table", "namespace": []string{"sales"}, "table": "orders"},
		"primary_key": []string{"id"},
		"dimensions":  []map[string]interface{}{{"name": "id", "type": "int64"}, {"name": "customer", "type": "int64"}},
		"joins":       []map[string]interface{}{{"to": "customers", "relationship": "many_to_one", "on": []map[string]string{{"from": "customer", "to": "cid"}}}},
	})
	metrics := []string{"ratio"}
	r, err := f.h.CompileExploreQuery(ctx, CompileExploreQueryRequestObject{
		Id: f.ws, Body: &ExploreCompileRequest{Query: ExploreQuery{Model: "orders", Metrics: &metrics}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := r.(CompileExploreQuery422JSONResponse)
	if !ok || got.Code != ErrorCodeNotSupported {
		t.Fatalf("compile = %+v, want 422 not_supported", r)
	}
}

// A v1 metric on a catalog the workspace no longer has a grant for must not
// leak the table's columns or become a model.
func TestSemanticModels_MigrateV1NeedsCatalogGrant(t *testing.T) {
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)
	space, err := f.h.queries.GetDefaultSpace(context.Background(), pgtype.UUID{Bytes: f.ws, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := pgtype.UUID{Bytes: uuid.MustParse(f.owner), Valid: true}
	if _, err := f.h.queries.CreateSemanticMetric(context.Background(), db.CreateSemanticMetricParams{
		WorkspaceID: pgtype.UUID{Bytes: f.ws, Valid: true}, SpaceID: space.ID, Slug: "secret_count", Label: "Secret",
		CatalogID: pgtype.UUID{Bytes: f.other, Valid: true}, Namespace: []string{"sales"}, TableName: "orders_secret",
		Definition: []byte(`{"type":"simple","aggregation":"count"}`), FormatVersion: 1, OwnerUserID: owner, UpdatedBy: owner,
	}); err != nil {
		t.Fatal(err)
	}
	apply := false
	report := migrate(t, f, ctx, MigrateV1Request{DryRun: &apply})
	if len(report.Items) != 1 || report.Items[0].ErrorCode == nil ||
		*report.Items[0].ErrorCode != MigrateV1ItemErrorCodeCatalogNotGranted {
		t.Fatalf("report = %+v", report.Items)
	}
	if len(report.Items[0].Columns) != 0 {
		t.Fatalf("columns leaked: %v", report.Items[0].Columns)
	}
	if got := listModels(t, f, ctx); len(got) != 0 {
		t.Fatalf("created %d models on an ungranted catalog", len(got))
	}
}
