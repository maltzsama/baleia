package trino

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithBasicAuth(t *testing.T) {
	t.Parallel()
	var user, pass, trinoUser string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ = r.BasicAuth()
		trinoUser = r.Header.Get("X-Trino-User")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	base := srv.Client()
	c := NewWithHTTPClient(srv.URL, "baleia", WithBasicAuth(base, "login", "pw"))
	if _, err := c.Execute(context.Background(), "SELECT 1", "", ""); err == nil {
		t.Fatal("want the 401 as an error")
	}
	if user != "login" || pass != "pw" || trinoUser != "baleia" {
		t.Errorf("basic auth %q/%q, X-Trino-User %q", user, pass, trinoUser)
	}
	resp, err := base.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if user != "" {
		t.Error("the base client must stay unauthenticated")
	}
}

// TestAuthenticated: the client trusts the coordinator's CA bundle and logs in
// with the password; without the bundle the certificate is refused.
func TestAuthenticated(t *testing.T) {
	t.Parallel()
	var user, pass string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))

	plain := &http.Client{Transport: &http.Transport{}}
	if resp, err := plain.Get(srv.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("an unknown CA must be refused")
	}

	hc, err := Authenticated(&http.Client{Transport: &http.Transport{}}, "login", "pw", caPEM)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("trusted CA refused: %v", err)
	}
	_ = resp.Body.Close()
	if user != "login" || pass != "pw" {
		t.Errorf("basic auth %q/%q", user, pass)
	}

	if _, err := Authenticated(&http.Client{Transport: &http.Transport{}}, "", "", "not a pem"); !errors.Is(err, ErrInvalidCA) {
		t.Errorf("bad bundle: %v", err)
	}
	if hc, err := Authenticated(plain, "", "", ""); err != nil || hc != plain {
		t.Errorf("nothing to apply must keep the client: %v", err)
	}
}

// TestWithBasicAuth_NoRedirect: a redirect is not followed, so the password
// is never sent to the redirect target.
func TestWithBasicAuth_NoRedirect(t *testing.T) {
	t.Parallel()
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, leaked = r.BasicAuth()
	}))
	defer target.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()
	resp, err := WithBasicAuth(srv.Client(), "login", "pw").Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || leaked {
		t.Errorf("status %d, password sent to the redirect target: %t", resp.StatusCode, leaked)
	}
}
