package api

import (
	"testing"
)

func TestSavedQueryVersionsAndTrash(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)

	created, err := f.h.CreateSavedQuery(ctx, CreateSavedQueryRequestObject{
		Id: f.ws, Body: &CreateSavedQueryRequest{Name: "Revenue", Sql: "SELECT 1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := created.(CreateSavedQuery201JSONResponse)

	sql := "SELECT 2"
	if _, err := f.h.UpdateSavedQuery(ctx, UpdateSavedQueryRequestObject{
		Id: f.ws, QueryId: q.Id, Body: &UpdateSavedQueryRequest{Sql: &sql},
	}); err != nil {
		t.Fatal(err)
	}

	list, err := f.h.ListSavedQueryVersions(ctx, ListSavedQueryVersionsRequestObject{Id: f.ws, QueryId: q.Id})
	if err != nil {
		t.Fatal(err)
	}
	versions := list.(ListSavedQueryVersions200JSONResponse)
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions = %+v", versions)
	}

	got, err := f.h.GetSavedQueryVersion(ctx, GetSavedQueryVersionRequestObject{Id: f.ws, QueryId: q.Id, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	detail := got.(GetSavedQueryVersion200JSONResponse)
	if detail.Snapshot["sql"] != "SELECT 1" || detail.Snapshot["name"] != "Revenue" {
		t.Fatalf("snapshot = %+v", detail.Snapshot)
	}

	if _, err := f.h.DeleteSavedQuery(ctx, DeleteSavedQueryRequestObject{Id: f.ws, QueryId: q.Id}); err != nil {
		t.Fatal(err)
	}
	trash, err := f.h.ListTrash(ctx, ListTrashRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	items := trash.(ListTrash200JSONResponse)
	if len(items) != 1 || items[0].EntityId != q.Id || items[0].Slug != "revenue" || items[0].EntityType != TrashItemEntityType("saved_query") {
		t.Fatalf("trash = %+v", items)
	}

	restored, err := f.h.RestoreSavedQueryVersion(ctx, RestoreSavedQueryVersionRequestObject{
		Id: f.ws, QueryId: q.Id, Version: 1, Body: &RestoreSavedQueryRequest{},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := restored.(RestoreSavedQueryVersion200JSONResponse)
	if r.Id != q.Id || r.Name != "Revenue" || r.Sql != "SELECT 1" {
		t.Fatalf("restored = %+v", r)
	}

	after, err := f.h.ListSavedQueryVersions(ctx, ListSavedQueryVersionsRequestObject{Id: f.ws, QueryId: q.Id})
	if err != nil {
		t.Fatal(err)
	}
	versions = after.(ListSavedQueryVersions200JSONResponse)
	if len(versions) != 4 || versions[0].ChangeKind != ContentVersionChangeKind("restore") || versions[0].RestoredFrom == nil || *versions[0].RestoredFrom != 1 {
		t.Fatalf("after restore = %+v", versions)
	}
}

func TestSemanticMetricVersions(t *testing.T) {
	t.Parallel()
	f, _ := metricFixture(t)
	ctx := f.as(f.owner)

	created, err := f.h.CreateSemanticMetric(ctx, CreateSemanticMetricRequestObject{
		Id: f.ws, Body: &SemanticMetricCreate{
			Slug: "net_revenue", Label: "Net revenue", CatalogId: f.lake,
			Namespace: []string{"sales"}, Table: "orders", Definition: simpleDefinition("sum", "net_revenue"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := created.(CreateSemanticMetric201JSONResponse)

	label := "Net revenue v2"
	if _, err := f.h.UpdateSemanticMetric(ctx, UpdateSemanticMetricRequestObject{
		Id: f.ws, MetricId: m.Id, Body: &SemanticMetricUpdate{Label: &label},
	}); err != nil {
		t.Fatal(err)
	}

	list, err := f.h.ListSemanticMetricVersions(ctx, ListSemanticMetricVersionsRequestObject{Id: f.ws, MetricId: m.Id})
	if err != nil {
		t.Fatal(err)
	}
	if versions := list.(ListSemanticMetricVersions200JSONResponse); len(versions) != 2 {
		t.Fatalf("versions = %+v", versions)
	}
	got, err := f.h.GetSemanticMetricVersion(ctx, GetSemanticMetricVersionRequestObject{Id: f.ws, MetricId: m.Id, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if detail := got.(GetSemanticMetricVersion200JSONResponse); detail.Snapshot["label"] != "Net revenue" {
		t.Fatalf("snapshot = %+v", detail.Snapshot)
	}

	if _, err := f.h.DeleteSemanticMetric(ctx, DeleteSemanticMetricRequestObject{Id: f.ws, MetricId: m.Id}); err != nil {
		t.Fatal(err)
	}
	trash, err := f.h.ListTrash(ctx, ListTrashRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	items := trash.(ListTrash200JSONResponse)
	if len(items) != 1 || items[0].EntityType != TrashItemEntityType("semantic_metric") {
		t.Fatalf("trash = %+v", items)
	}

	restored, err := f.h.RestoreSemanticMetricVersion(ctx, RestoreSemanticMetricVersionRequestObject{
		Id: f.ws, MetricId: m.Id, Version: 1, Body: &RestoreSemanticMetricRequest{},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := restored.(RestoreSemanticMetricVersion200JSONResponse)
	if r.Id != m.Id || r.Label != "Net revenue" {
		t.Fatalf("restored = %+v", r)
	}
}
