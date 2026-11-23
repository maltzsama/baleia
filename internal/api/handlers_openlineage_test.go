package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/openlineage"
)

type lineageAuditRecorder struct{ events []audit.Event }

type failingLineageReads struct {
	lineageReads
	calls int
}

func (r *failingLineageReads) GetMetadataTableByRef(ctx context.Context, arg db.GetMetadataTableByRefParams) (db.GetMetadataTableByRefRow, error) {
	r.calls++
	if r.calls == 2 {
		return db.GetMetadataTableByRefRow{}, errors.New("database unavailable")
	}
	return r.lineageReads.GetMetadataTableByRef(ctx, arg)
}

func (r *lineageAuditRecorder) Record(_ context.Context, e audit.Event) error {
	r.events = append(r.events, e)
	return nil
}

func TestOpenLineageDestinationAndExport(t *testing.T) {
	t.Parallel()
	f := newLineageFixture(t)
	recorder := &lineageAuditRecorder{}
	f.h.audit = recorder
	keyring, err := crypto.NewKeyring(strings.Repeat("k", crypto.MinKeyLen), "")
	if err != nil {
		t.Fatal(err)
	}
	f.h.keyring = keyring
	f.h.core.dialer, err = connections.NewDialer(connections.DialerOptions{AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	var sent atomic.Int32
	var failMetric atomic.Bool
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request")
		}
		var event openlineage.RunEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil || event.EventType != "COMPLETE" || len(event.Inputs) != 1 || len(event.Outputs) != 1 {
			t.Errorf("invalid event: %+v %v", event, err)
		}
		sent.Add(1)
		if failMetric.Load() && strings.Contains(event.Job.Name, "/metric/") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer collector.Close()
	ctx := f.as(f.owner)
	denied, err := f.h.GetOpenLineageDestination(f.as(f.stranger), GetOpenLineageDestinationRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := denied.(GetOpenLineageDestination404JSONResponse); !ok {
		t.Fatalf("stranger response = %T", denied)
	}
	invalid, err := f.h.PutOpenLineageDestination(ctx, PutOpenLineageDestinationRequestObject{Id: f.ws, Body: &UpdateOpenLineageDestination{Endpoint: "http://user:password@example.com", Namespace: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := invalid.(PutOpenLineageDestination422JSONResponse); !ok {
		t.Fatalf("invalid destination response = %T", invalid)
	}
	token := "secret"
	insecure, err := f.h.PutOpenLineageDestination(ctx, PutOpenLineageDestinationRequestObject{Id: f.ws, Body: &UpdateOpenLineageDestination{Endpoint: "http://example.test/events", Namespace: "test", Token: &token}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := insecure.(PutOpenLineageDestination422JSONResponse); !ok {
		t.Fatalf("insecure destination response = %T", insecure)
	}
	resp, err := f.h.PutOpenLineageDestination(ctx, PutOpenLineageDestinationRequestObject{Id: f.ws, Body: &UpdateOpenLineageDestination{Endpoint: collector.URL + "/events", Namespace: "test", Token: &token}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(PutOpenLineageDestination200JSONResponse); !ok {
		t.Fatalf("put = %T", resp)
	}
	get, err := f.h.GetOpenLineageDestination(ctx, GetOpenLineageDestinationRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := get.(GetOpenLineageDestination200JSONResponse); !ok || !got.Configured || got.Endpoint != collector.URL+"/events" {
		t.Fatalf("get = %#v", get)
	}
	exported, err := f.h.ExportOpenLineage(ctx, ExportOpenLineageRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := exported.(ExportOpenLineage200JSONResponse); !ok || got.Sent != 3 || got.Failed != 0 || sent.Load() != 3 {
		t.Fatalf("export = %#v, sent = %d", exported, sent.Load())
	}
	kind := ExportOpenLineageParamsKindMetric
	filtered, err := f.h.ExportOpenLineage(ctx, ExportOpenLineageRequestObject{Id: f.ws, Params: ExportOpenLineageParams{Kind: &kind}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := filtered.(ExportOpenLineage200JSONResponse); !ok || got.Sent != 1 || got.Failed != 0 || sent.Load() != 4 {
		t.Fatalf("filtered export = %#v", filtered)
	}
	originalLineage := f.h.CatalogHandlers.lineage
	f.h.CatalogHandlers.lineage = &failingLineageReads{lineageReads: originalLineage}
	_, err = f.h.ExportOpenLineage(ctx, ExportOpenLineageRequestObject{Id: f.ws})
	f.h.CatalogHandlers.lineage = originalLineage
	if err == nil || sent.Load() != 4 {
		t.Fatalf("building events: error=%v, requests=%d", err, sent.Load())
	}
	failMetric.Store(true)
	partial, err := f.h.ExportOpenLineage(ctx, ExportOpenLineageRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := partial.(ExportOpenLineage200JSONResponse); !ok || got.Sent != 2 || got.Failed != 1 {
		t.Fatalf("partial export = %#v", partial)
	}
	deleted, err := f.h.DeleteOpenLineageDestination(ctx, DeleteOpenLineageDestinationRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := deleted.(DeleteOpenLineageDestination204Response); !ok {
		t.Fatalf("delete = %T", deleted)
	}
	if len(recorder.events) != 5 || recorder.events[1].Action != audit.LineageExport || recorder.events[2].Action != audit.LineageExport || recorder.events[3].Action != audit.LineageExport {
		t.Fatalf("audit events = %+v", recorder.events)
	}
}
