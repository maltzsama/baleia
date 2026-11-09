package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func newWorkspaceFor(t *testing.T, f *metaFixture, name string) openapi_types.UUID {
	t.Helper()
	owner := model.UUID(f.owner)
	ws := model.Workspace{
		ID: model.UUID(repo.NewUUID()), Name: name, Type: "personal",
		PolarisCatalogName: model.Text(name), OwnerUserID: &owner,
	}
	if err := f.gdb.Create(&ws).Error; err != nil {
		t.Fatal(err)
	}
	addDefaultSpace(t, f.gdb, ws.ID)
	return openapi_types.UUID(repo.PGUUID(string(ws.ID)).Bytes)
}

func TestBundleExportImport(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)

	source := createSpace(t, f, ctx, CreateSpaceRequest{Name: "Source", Visibility: CreateSpaceRequestVisibilityWorkspace})
	folder := model.QueryFolder{
		ID: model.UUID(repo.NewUUID()), WorkspaceID: model.UUID(f.ws.String()),
		SpaceID: model.UUID(source.Id.String()), Name: "Monthly", Slug: "monthly",
	}
	if err := f.gdb.Create(&folder).Error; err != nil {
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
	items := bad["items"].([]any)
	bad["items"] = append(items, map[string]any{"kind": "model", "slug": "nope"})
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
