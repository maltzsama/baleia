package icebergrest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestBearer_ReuseWindow: a client-credentials token is reused until 30 s
// before it expires, or for half its life when it lives less than that, so a
// caller never gets a token past its expiry.
func TestBearer_ReuseWindow(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		expiresIn int
		want      time.Duration
	}{
		{"an hour", 3600, 3600*time.Second - 30*time.Second},
		{"twenty seconds", 20, 10 * time.Second},
		{"no stated lifetime", 0, time.Minute},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = r.ParseForm()
				if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "id" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": tt.expiresIn})
			}))
			defer srv.Close()
			c := New(srv.URL, Auth{ClientID: "id", ClientSecret: "s"}, nil, srv.Client())
			start := time.Now()
			tok, expiry, err := c.Bearer(context.Background())
			if err != nil || tok != "tok" {
				t.Fatalf("Bearer = %q, %v", tok, err)
			}
			if got := expiry.Sub(start); got > tt.want+time.Second || got < tt.want-time.Second {
				t.Errorf("reused for %s, want %s", got, tt.want)
			}
			if _, _, err := c.Bearer(context.Background()); err != nil || calls != 1 {
				t.Errorf("second call fetched again (%d calls, %v)", calls, err)
			}
		})
	}
}

func TestBearer_StaticAndAnonymous(t *testing.T) {
	t.Parallel()
	tok, expiry, err := New("http://x", Auth{Token: "static"}, nil, http.DefaultClient).Bearer(context.Background())
	if err != nil || tok != "static" || !expiry.IsZero() {
		t.Errorf("static = %q, %v, %v", tok, expiry, err)
	}
	tok, _, err = New("http://x", Auth{}, nil, http.DefaultClient).Bearer(context.Background())
	if err != nil || tok != "" {
		t.Errorf("anonymous = %q, %v", tok, err)
	}
}
