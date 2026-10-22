package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestParseRetrieverCron_IsStandardFiveField pins #139: the expression is
// "minute hour day-of-month month day-of-week", not "second minute hour ...".
func TestParseRetrieverCron_IsStandardFiveField(t *testing.T) {
	s, _, err := parseRetrieverCron("0 3 * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	from := time.Date(2026, 10, 13, 19, 10, 0, 0, time.UTC)
	if got, want := s.Next(from), time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("next run of '0 3 * * *' after %s = %s, want %s (daily at 03:00, not hourly at :03)", from, got, want)
	}
	s, _, err = parseRetrieverCron("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Friday 17:50 -> next is Monday 09:00.
	fri := time.Date(2026, 10, 16, 17, 50, 0, 0, time.UTC)
	if got, want := s.Next(fri), time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("weekday window next = %s, want %s", got, want)
	}
}

func TestParseRetrieverCron_Rejects(t *testing.T) {
	for name, expr := range map[string]string{
		"every minute":      "* * * * *",
		"every 4 minutes":   "*/4 * * * *",
		"six fields":        "0 0 3 * * *",
		"garbage":           "nope",
		"empty":             "",
		"out of range hour": "0 25 * * *",
	} {
		if _, _, err := parseRetrieverCron(expr); err == nil {
			t.Errorf("%s: %q was accepted", name, expr)
		}
	}
	for _, ok := range []string{"*/5 * * * *", "0 */6 * * *", "30 2 * * 0"} {
		if _, _, err := parseRetrieverCron(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}

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
	for name, expr := range map[string]string{"every minute": "* * * * *", "invalid": "not a cron"} {
		f := &updateRetrieverFake{}
		h := &Handlers{dialects: dialect.NewRegistry(), queries: f, tx: directTx{q: f}}
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
	h := &Handlers{dialects: dialect.NewRegistry(), queries: f, tx: directTx{q: f}}
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
	inc := TriggerRetrieverRefreshJSONBodyRefreshType("incremental")
	full := TriggerRetrieverRefreshJSONBodyRefreshType("full")

	tc := &recordingTemporal{}
	h := &Handlers{dialects: dialect.NewRegistry(), queries: &updateRetrieverFake{}, temporalClient: tc}
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
