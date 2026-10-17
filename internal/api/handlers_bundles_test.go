package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/galileostudio/baleia/internal/content"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func newWorkspaceFor(t *testing.T, f *metaFixture, name string) openapi_types.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ($1, 'personal', $2, $3) RETURNING id`,
		name, name, mustUUID(t, f.owner)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return openapi_types.UUID(id.Bytes)
}

func TestBundleExportImport(t *testing.T) {
	f := newMetaFixture(t)
	ctx := f.as(f.owner)

	source := createSpace(t, f, ctx, CreateSpaceRequest{Name: "Source", Visibility: CreateSpaceRequestVisibilityWorkspace})
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO query_folders (workspace_id, space_id, name, slug) VALUES ($1, $2, 'Monthly', 'monthly')`,
		pgUUIDOf(f.ws), pgUUIDOf(source.Id)); err != nil {
		t.Fatal(err)
	}
	if r, err := f.h.CreateSavedQuery(ctx, CreateSavedQueryRequestObject{
		Id: f.ws, Body: &CreateSavedQueryRequest{Name: "Revenue", Sql: "SELECT 1", SpaceId: &source.Id},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(CreateSavedQuery201JSONResponse); !ok {
		t.Fatalf("create query: %T", r)
	}

	exported, err := f.h.ExportSpaceBundle(ctx, ExportSpaceBundleRequestObject{Id: f.ws, SpaceId: source.Id})
	if err != nil {
		t.Fatal(err)
	}
	raw := exported.(exportBundleResponse).body
	var bundle content.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Format != content.BundleFormat || len(bundle.Items) != 2 {
		t.Fatalf("bundle = %+v", bundle)
	}

	// A different workspace: slugs are unique per workspace for queries.
	ws2 := newWorkspaceFor(t, f, "other")
	dest := createSpaceIn(t, f, ctx, ws2, CreateSpaceRequest{Name: "Dest", Visibility: CreateSpaceRequestVisibilityWorkspace})

	var body ImportSpaceBundleJSONRequestBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	apply := ImportSpaceBundleParamsMode("apply")
	r, err := f.h.ImportSpaceBundle(ctx, ImportSpaceBundleRequestObject{
		Id: ws2, SpaceId: dest.Id, Body: &body, Params: ImportSpaceBundleParams{Mode: &apply},
	})
	if err != nil {
		t.Fatal(err)
	}
	report := r.(importReportResponse)
	if report.status != http.StatusOK || !report.report.OK {
		t.Fatalf("apply = %d %+v", report.status, report.report)
	}
	for _, it := range report.report.Items {
		if it.Action != content.ActionCreate {
			t.Fatalf("apply action = %s for %s/%s", it.Action, it.Kind, it.Slug)
		}
	}

	// Re-importing the same bundle changes nothing.
	r, err = f.h.ImportSpaceBundle(ctx, ImportSpaceBundleRequestObject{
		Id: ws2, SpaceId: dest.Id, Body: &body, Params: ImportSpaceBundleParams{Mode: &apply},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range r.(importReportResponse).report.Items {
		if it.Action != content.ActionNoChanges {
			t.Fatalf("re-import action = %s for %s/%s", it.Action, it.Kind, it.Slug)
		}
	}

	// A dry run reports without writing.
	var dryBody ImportSpaceBundleJSONRequestBody
	_ = json.Unmarshal(raw, &dryBody)
	dry := ImportSpaceBundleParamsMode("dry_run")
	r, err = f.h.ImportSpaceBundle(ctx, ImportSpaceBundleRequestObject{
		Id: ws2, SpaceId: dest.Id, Body: &dryBody, Params: ImportSpaceBundleParams{Mode: &dry},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.(importReportResponse); got.status != http.StatusOK || !got.report.OK {
		t.Fatalf("dry run = %d %+v", got.status, got.report)
	}

	// An unknown kind is an ERROR and apply answers 422, writing nothing.
	bad := ImportSpaceBundleJSONRequestBody{}
	_ = json.Unmarshal(raw, &bad)
	items := bad["items"].([]interface{})
	bad["items"] = append(items, map[string]interface{}{"kind": "model", "slug": "nope"})
	r, err = f.h.ImportSpaceBundle(ctx, ImportSpaceBundleRequestObject{
		Id: ws2, SpaceId: dest.Id, Body: &bad, Params: ImportSpaceBundleParams{Mode: &apply},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.(importReportResponse); got.status != http.StatusUnprocessableEntity || got.report.OK {
		t.Fatalf("bad apply = %d %+v", got.status, got.report)
	}
}
