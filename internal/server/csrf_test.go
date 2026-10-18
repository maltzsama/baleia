package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
)

func csrfFixture(called *bool) func(context.Context, http.ResponseWriter, *http.Request, interface{}) (interface{}, error) {
	return func(context.Context, http.ResponseWriter, *http.Request, interface{}) (interface{}, error) {
		*called = true
		return nil, nil
	}
}

func TestRefreshRequiresCSRFHeader(t *testing.T) {
	called := false
	h := csrfMiddleware(csrfFixture(&called), "RefreshToken")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	resp, err := h(context.Background(), rec, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if called || resp != nil || rec.Code != http.StatusForbidden {
		t.Fatalf("without header: called=%v resp=%v status=%d, want 403", called, resp, rec.Code)
	}

	called = false
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set(auth.CSRFHeader, auth.CSRFValue)
	rec = httptest.NewRecorder()
	resp, err = h(context.Background(), rec, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("the handler did not run with the CSRF header")
	}
	if resp != nil {
		t.Errorf("handler result = %v, want nil", resp)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the handler's 200", rec.Code)
	}
}

func TestLogoutRequiresCSRFHeader(t *testing.T) {
	called := false
	h := csrfMiddleware(csrfFixture(&called), "Logout")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	if _, err := h(context.Background(), rec, req, nil); err != nil {
		t.Fatal(err)
	}
	if called || rec.Code != http.StatusForbidden {
		t.Fatalf("without header: called=%v status=%d, want 403", called, rec.Code)
	}
}

func TestCSRFHeaderNotRequiredElsewhere(t *testing.T) {
	called := false
	h := csrfMiddleware(csrfFixture(&called), "Login")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	if _, err := h(context.Background(), rec, req, nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("login must not require the CSRF header")
	}
}
