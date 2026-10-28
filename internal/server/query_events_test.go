package server

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestMountQueryEvents: with ingestion off the event path answers 404, never
// the SPA fallback's 200, which Trino's listener would take for a delivery
// (#406); with it on, the ingester gets the POST.
func TestMountQueryEvents(t *testing.T) {
	t.Parallel()
	ingester := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for name, tc := range map[string]struct {
		events http.Handler
		want   int
	}{
		"off": {nil, http.StatusNotFound},
		"on":  {ingester, http.StatusNoContent},
	} {
		mux := chi.NewRouter()
		mountQueryEvents(mux, tc.events)
		mux.Get("/*", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<app-shell>")) })
		mux.Post("/*", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<app-shell>")) })
		if rec := serve(mux, http.MethodPost, "/internal/trino/query-events"); rec.Code != tc.want {
			t.Errorf("%s: POST = %d, want %d", name, rec.Code, tc.want)
		}
	}
}
