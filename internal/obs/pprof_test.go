package obs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPprofMux: the profiles are served on their own mux (#380).
func TestPprofMux(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	PprofMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "goroutine") {
		t.Fatalf("index = %d %q, want the profile list", rec.Code, rec.Body.String())
	}
}
