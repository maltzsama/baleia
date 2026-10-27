package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/jackc/pgx/v5/pgtype"
)

type updateRetrieverFake struct {
	fakeRetrieverQuerier
	updated *db.UpdateRetrieverParams
}

func (f *updateRetrieverFake) GetRetrieverByID(_ context.Context, p db.GetRetrieverByIDParams) (db.Retriever, error) {
	return db.Retriever{ID: p.ID, WorkspaceID: p.WorkspaceID, Name: "daily", DefinitionSql: "SELECT 1", DestCatalog: "tpch", DestNamespace: "sf1", DestTable: "daily", CronSchedule: "0 3 * * *", RefreshType: "full", Status: "active"}, nil
}

func (f *updateRetrieverFake) UpdateRetriever(_ context.Context, p db.UpdateRetrieverParams) (db.Retriever, error) {
	f.updated = &p
	return db.Retriever{ID: p.ID, CronSchedule: p.CronSchedule}, nil
}

// TestUpdateRetriever_CronValidation pins #140: update enforces the same rules as
// create (422, 5-minute minimum), and a valid schedule recomputes next_refresh_at.
func TestUpdateRetriever_CronValidation(t *testing.T) {
	t.Parallel()
	for name, expr := range map[string]string{"every minute": "* * * * *", "invalid": "not a cron"} {
		f := &updateRetrieverFake{}
		h := testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: f, tx: directTx{q: f}})
		bad := expr
		resp, err := h.UpdateRetriever(testContext(), UpdateRetrieverRequestObject{
			Id: PathId{0xAA}, RetrieverId: PathId{0xBB}, Body: &UpdateRetrieverJSONRequestBody{CronSchedule: &bad},
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, ok := resp.(UpdateRetriever422JSONResponse)
		if !ok || got.Code != "validation_error" {
			t.Errorf("%s: response %T %+v, want 422 validation_error", name, resp, resp)
		}
		if f.updated != nil {
			t.Errorf("%s: the retriever was updated despite an invalid schedule", name)
		}
	}

	f := &updateRetrieverFake{}
	h := testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: f, tx: directTx{q: f}})
	good := "30 2 * * *"
	if _, err := h.UpdateRetriever(testContext(), UpdateRetrieverRequestObject{
		Id: PathId{0xAA}, RetrieverId: PathId{0xBB}, Body: &UpdateRetrieverJSONRequestBody{CronSchedule: &good},
	}); err != nil {
		t.Fatal(err)
	}
	if f.updated == nil || f.updated.CronSchedule != good || f.updated.NextRefreshAt.Time.Minute() != 30 || f.updated.NextRefreshAt.Time.Hour() != 2 {
		t.Fatalf("update params = %+v", f.updated)
	}
	_ = pgtype.UUID{}
}

// TestTriggerRetrieverRefresh_OnlyFull pins #143: the manual trigger rejects
// incremental like create and update do, and starts no workflow for it.
func TestTriggerRetrieverRefresh_OnlyFull(t *testing.T) {
	t.Parallel()
	inc := TriggerRetrieverRefreshJSONBodyRefreshType("incremental")
	full := TriggerRetrieverRefreshJSONBodyRefreshType("full")

	tc := &recordingTemporal{}
	h := testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: &updateRetrieverFake{}, temporalClient: tc})
	req := func(rt *TriggerRetrieverRefreshJSONBodyRefreshType) TriggerRetrieverRefreshRequestObject {
		return TriggerRetrieverRefreshRequestObject{Id: PathId{0xAA}, RetrieverId: PathId{0xBB}, Body: &TriggerRetrieverRefreshJSONRequestBody{RefreshType: rt}}
	}

	resp, err := h.TriggerRetrieverRefresh(testContext(), req(&inc))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := resp.(TriggerRetrieverRefresh422JSONResponse); !ok || got.Code != "not_supported" {
		t.Fatalf("incremental: %T %+v, want 422 not_supported", resp, resp)
	}
	if len(tc.calls) != 0 {
		t.Fatal("a workflow was started for an incremental refresh")
	}

	for name, body := range map[string]*TriggerRetrieverRefreshJSONBodyRefreshType{"explicit full": &full, "default (retriever is full)": nil} {
		resp, err := h.TriggerRetrieverRefresh(testContext(), req(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := resp.(TriggerRetrieverRefresh202JSONResponse); !ok {
			t.Fatalf("%s: %T, want 202", name, resp)
		}
	}
	if len(tc.calls) != 2 || tc.calls[0][1] != "full" {
		t.Fatalf("workflow starts = %v, want two full refreshes", tc.calls)
	}
}
