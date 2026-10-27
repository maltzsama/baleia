package trino

import (
	"context"
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
