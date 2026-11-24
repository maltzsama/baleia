package datapolicy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type opaStore struct {
	*memStore
	statuses []EngineStatus
}

func (s *opaStore) RecordEngineStatus(_ context.Context, st EngineStatus) error {
	s.statuses = append(s.statuses, st)
	return nil
}

func opaHandler(t *testing.T) (*OPAHandler, *opaStore, *memObjects, *Publisher, uuid.UUID) {
	t.Helper()
	now := time.Now()
	store, objects := &opaStore{memStore: newMemStore()}, &memObjects{}
	p := newPublisher(t, store.memStore, objects, &now)
	engine := uuid.New()
	h := &OPAHandler{
		Store: store, Objects: objects, Token: "opa-token-0123456789",
		EngineExists: func(_ context.Context, id uuid.UUID) (bool, error) { return id == engine, nil },
	}
	return h, store, objects, p, engine
}

func get(h *OPAHandler, token, ifNoneMatch string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, BundlePath, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	rec := httptest.NewRecorder()
	h.ServeBundle(rec, req)
	return rec
}

func TestOPAHandler_ServeBundle(t *testing.T) {
	t.Parallel()
	h, store, objects, p, _ := opaHandler(t)
	if rec := get(h, "wrong", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", rec.Code)
	}
	if rec := get(h, h.Token, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("nothing published = %d, want 404", rec.Code)
	}
	sales := uuid.New()
	store.policies[sales] = publishedPolicies(sales)
	rev, err := p.PublishCatalog(context.Background(), sales, []string{"lake"}, ViewInspection{}, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	rec := get(h, h.Token, "")
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"`+rev.ID.String()+`"` || rec.Body.String() != string(objects.objects[BundleKey(rev.ID)]) {
		t.Fatalf("bundle = %d %q, want the stored bundle with the revision as ETag", rec.Code, rec.Header().Get("ETag"))
	}
	if rec := get(h, h.Token, `"`+rev.ID.String()+`"`); rec.Code != http.StatusNotModified {
		t.Fatalf("unchanged revision = %d, want 304", rec.Code)
	}
	off := *h
	off.Token = ""
	if rec := get(&off, "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("endpoints off = %d, want 404", rec.Code)
	}
}

func TestOPAHandler_ReceiveStatus(t *testing.T) {
	t.Parallel()
	h, store, _, _, engine := opaHandler(t)
	post := func(token, body string) int {
		req := httptest.NewRequest(http.MethodPost, StatusPath, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ReceiveStatus(rec, req)
		return rec.Code
	}
	report := `{"labels":{"id":"x","baleia_engine_id":"` + engine.String() + `"},
		"bundles":{"baleia":{"active_revision":"r1","code":"bundle_error","message":"signature"},"policy":{"active_revision":"d1"}}}`
	if code := post("wrong", report); code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", code)
	}
	if code := post(h.Token, report); code != http.StatusNoContent {
		t.Fatalf("report = %d", code)
	}
	got := store.statuses[0]
	if got.EngineID != engine || got.ActiveRevision != "r1" || got.PolicyDigest != "d1" || got.Code != "bundle_error" || got.Message != "signature" {
		t.Fatalf("recorded = %+v", got)
	}
	if code := post(h.Token, `{"labels":{}}`); code != http.StatusBadRequest {
		t.Fatalf("no engine label = %d", code)
	}
	if code := post(h.Token, `{"labels":{"baleia_engine_id":"`+uuid.NewString()+`"}}`); code != http.StatusNotFound {
		t.Fatalf("unknown engine = %d", code)
	}
	if code := post(h.Token, `not json`); code != http.StatusBadRequest {
		t.Fatalf("garbage = %d", code)
	}
}
