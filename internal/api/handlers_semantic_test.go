package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/semantic/docs"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func tags(v ...string) *[]string { return &v }

func strPtr(s string) *string { return &s }

func pgUUIDOf(id openapi_types.UUID) db.UUID {
	return db.UUID{Bytes: id, Valid: true}
}

func upsertDoc(t *testing.T, f *metaFixture, ctx context.Context, catalog openapi_types.UUID, column *string, body SemanticDocUpsert) UpsertSemanticDocResponseObject {
	t.Helper()
	got, err := f.h.UpsertSemanticDoc(ctx, UpsertSemanticDocRequestObject{
		Id: f.ws,
		Params: UpsertSemanticDocParams{
			CatalogId: catalog, Ns: []string{"sales"}, Table: "orders", Column: column,
		},
		Body: &body,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSemanticDocs_UpsertListDelete(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)

	tableDoc := upsertDoc(t, f, ctx, f.lake, nil, SemanticDocUpsert{
		Description: "Orders, one row per order.", Tags: tags("certified", "sales"),
	})
	doc, ok := tableDoc.(UpsertSemanticDoc200JSONResponse)
	if !ok {
		t.Fatalf("upsert table doc: %T", tableDoc)
	}
	if doc.Table != "orders" || doc.Column != nil || doc.Description == "" || len(doc.Tags) != 2 {
		t.Fatalf("table doc = %+v", doc)
	}

	col := "id"
	columnDoc := upsertDoc(t, f, ctx, f.lake, &col, SemanticDocUpsert{Description: "Order id."})
	if _, ok := columnDoc.(UpsertSemanticDoc200JSONResponse); !ok {
		t.Fatalf("upsert column doc: %T", columnDoc)
	}

	list, err := f.h.ListSemanticDocs(ctx, ListSemanticDocsRequestObject{
		Id: f.ws, Params: ListSemanticDocsParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders"},
	})
	if err != nil {
		t.Fatal(err)
	}
	docs, ok := list.(ListSemanticDocs200JSONResponse)
	if !ok {
		t.Fatalf("list: %T", list)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %+v", docs)
	}
	if docs[0].Column != nil || docs[0].Description != "Orders, one row per order." {
		t.Fatalf("table-level doc should be first: %+v", docs)
	}

	// A column filter returns only that doc.
	one, err := f.h.ListSemanticDocs(ctx, ListSemanticDocsRequestObject{
		Id: f.ws, Params: ListSemanticDocsParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders", Column: &col},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := one.(ListSemanticDocs200JSONResponse); len(got) != 1 || got[0].Column == nil || *got[0].Column != "id" {
		t.Fatalf("column filter = %+v", got)
	}

	// Upsert replaces in place (natural key), it does not duplicate.
	upsertDoc(t, f, ctx, f.lake, nil, SemanticDocUpsert{Description: "Updated.", Tags: tags("certified")})
	list, _ = f.h.ListSemanticDocs(ctx, ListSemanticDocsRequestObject{
		Id: f.ws, Params: ListSemanticDocsParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders"},
	})
	if got := list.(ListSemanticDocs200JSONResponse); len(got) != 2 || got[0].Description != "Updated." {
		t.Fatalf("after upsert = %+v", got)
	}

	if r, err := f.h.DeleteSemanticDoc(ctx, DeleteSemanticDocRequestObject{
		Id: f.ws, Params: DeleteSemanticDocParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders", Column: &col},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(DeleteSemanticDoc204Response); !ok {
		t.Fatalf("delete: %T", r)
	}
	// Deleting it again is a 404.
	if r, err := f.h.DeleteSemanticDoc(ctx, DeleteSemanticDocRequestObject{
		Id: f.ws, Params: DeleteSemanticDocParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders", Column: &col},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(DeleteSemanticDoc404JSONResponse); !ok {
		t.Fatalf("second delete: %T", r)
	}
	// GET of the missing column doc is a 404.
	if r, err := f.h.ListSemanticDocs(ctx, ListSemanticDocsRequestObject{
		Id: f.ws, Params: ListSemanticDocsParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders", Column: &col},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(ListSemanticDocs404JSONResponse); !ok {
		t.Fatalf("missing doc: %T", r)
	}
}

func TestSemanticDocs_UngrantedCatalog404(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)
	if r := upsertDoc(t, f, ctx, f.other, nil, SemanticDocUpsert{Description: "secret"}); !isSemantic404(r) {
		t.Fatalf("upsert on ungranted catalog: %T", r)
	}
	list, err := f.h.ListSemanticDocs(ctx, ListSemanticDocsRequestObject{
		Id: f.ws, Params: ListSemanticDocsParams{CatalogId: f.other, Ns: []string{"sales"}, Table: "orders_secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := list.(ListSemanticDocs404JSONResponse); !ok {
		t.Fatalf("list on ungranted catalog: %T", list)
	}
	del, err := f.h.DeleteSemanticDoc(ctx, DeleteSemanticDocRequestObject{
		Id: f.ws, Params: DeleteSemanticDocParams{CatalogId: f.other, Ns: []string{"sales"}, Table: "orders_secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := del.(DeleteSemanticDoc404JSONResponse); !ok {
		t.Fatalf("delete on ungranted catalog: %T", del)
	}
}

func TestSemanticDocs_ForeignWorkspace404(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.stranger)
	list, err := f.h.ListSemanticDocs(ctx, ListSemanticDocsRequestObject{
		Id: f.ws, Params: ListSemanticDocsParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := list.(ListSemanticDocs404JSONResponse); !ok {
		t.Fatalf("foreign workspace list: %T", list)
	}
	up, err := f.h.UpsertSemanticDoc(ctx, UpsertSemanticDocRequestObject{
		Id: f.ws, Params: UpsertSemanticDocParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders"},
		Body: &SemanticDocUpsert{Description: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := up.(UpsertSemanticDoc404JSONResponse); !ok {
		t.Fatalf("foreign workspace upsert: %T", up)
	}
}

// TestSemanticDocs_SurviveResync: docs use the natural key, so a metadata
// resync that drops and recreates the table row does not lose them.
func TestSemanticDocs_SurviveResync(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)
	upsertDoc(t, f, ctx, f.lake, nil, SemanticDocUpsert{Description: "Survives."})

	if err := f.gdb.Where("id = ?", repo.UUIDString(pgUUIDOf(f.orders))).Delete(&model.MetadataTable{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.gdb.Create(&model.MetadataTable{
		ID: model.UUID(repo.NewUUID()), CatalogID: model.UUID(repo.UUIDString(pgUUIDOf(f.lake))),
		Namespace: model.StringArray{"sales"}, Name: "orders", Kind: "table", Fingerprint: "new",
		LastSeenAt: time.Now(), LastChangedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	list, err := f.h.ListSemanticDocs(ctx, ListSemanticDocsRequestObject{
		Id: f.ws, Params: ListSemanticDocsParams{CatalogId: f.lake, Ns: []string{"sales"}, Table: "orders"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := list.(ListSemanticDocs200JSONResponse)
	if len(got) != 1 || got[0].Description != "Survives." {
		t.Fatalf("doc lost across resync: %+v", got)
	}
}

func TestSemanticDocs_Search(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)
	upsertDoc(t, f, ctx, f.lake, nil, SemanticDocUpsert{Description: "Orders, one row per order.", Tags: tags("certified")})

	// A doc on an ungranted catalog must not leak through search.
	if err := f.gdb.Create(&model.SemanticDoc{
		ID: model.UUID(repo.NewUUID()), WorkspaceID: model.UUID(repo.UUIDString(pgUUIDOf(f.ws))),
		CatalogID: model.UUID(repo.UUIDString(pgUUIDOf(f.other))), Namespace: model.StringArray{"sales"},
		TableNameValue: "orders_secret", Description: "Orders secret",
		Tags: model.StringArray{"certified"}, UpdatedBy: model.UUID(f.owner),
	}).Error; err != nil {
		t.Fatal(err)
	}

	search := func(params SearchSemanticDocsParams) SearchSemanticDocs200JSONResponse {
		t.Helper()
		r, err := f.h.SearchSemanticDocs(ctx, SearchSemanticDocsRequestObject{Id: f.ws, Params: params})
		if err != nil {
			t.Fatal(err)
		}
		out, ok := r.(SearchSemanticDocs200JSONResponse)
		if !ok {
			t.Fatalf("search: %T", r)
		}
		return out
	}

	all := search(SearchSemanticDocsParams{Q: "orders"})
	if len(all) != 1 || all[0].Table != "orders" {
		t.Fatalf("search leaked or missed: %+v", all)
	}
	if got := search(SearchSemanticDocsParams{Q: "orders", Tag: strPtr("certified")}); len(got) != 1 {
		t.Fatalf("tag filter = %+v", got)
	}
	if got := search(SearchSemanticDocsParams{Q: "orders", Tag: strPtr("nope")}); len(got) != 0 {
		t.Fatalf("tag filter should exclude: %+v", got)
	}

	// A short query is a 422.
	if r, err := f.h.SearchSemanticDocs(ctx, SearchSemanticDocsRequestObject{Id: f.ws, Params: SearchSemanticDocsParams{Q: "o"}}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(SearchSemanticDocs422JSONResponse); !ok {
		t.Fatalf("short query: %T", r)
	}
}

func TestSemanticDocs_DescriptionTooLong(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)
	r := upsertDoc(t, f, ctx, f.lake, nil, SemanticDocUpsert{Description: strings.Repeat("x", docs.MaxDescription+1)})
	if _, ok := r.(UpsertSemanticDoc422JSONResponse); !ok {
		t.Fatalf("too long: %T", r)
	}
}

func isSemantic404(r any) bool {
	switch r.(type) {
	case UpsertSemanticDoc404JSONResponse, ListSemanticDocs404JSONResponse, DeleteSemanticDoc404JSONResponse:
		return true
	}
	return false
}
