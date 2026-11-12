package icebergrest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
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

// TestLoadTable_BadRequestIsUnsupported: Unity answers a plain Delta table with
// 400. Only a reader built for it reports that as ErrTableUnsupported; every
// other reader keeps the plain status error, and other statuses stay errors.
func TestLoadTable_BadRequestIsUnsupported(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/config":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/v1/namespaces/s/tables/delta":
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	list := func(context.Context) ([]connections.CatalogRef, error) { return nil, nil }
	same := func(c string) string { return c }
	ref := func(name string) connections.TableRef {
		return connections.TableRef{Catalog: "c", Namespace: connections.Namespace{"s"}, Name: name}
	}
	ctx := context.Background()

	tolerant := New(srv.URL, Auth{}, nil, srv.Client()).ReaderSkippingUnsupported(list, same, nil)
	if _, err := tolerant.LoadTable(ctx, ref("delta")); !errors.Is(err, connections.ErrTableUnsupported) {
		t.Errorf("400 on a tolerant reader = %v, want ErrTableUnsupported", err)
	}
	if _, err := tolerant.LoadTable(ctx, ref("broken")); err == nil || errors.Is(err, connections.ErrTableUnsupported) {
		t.Errorf("500 on a tolerant reader = %v, want a plain error", err)
	}
	strict := New(srv.URL, Auth{}, nil, srv.Client()).ReaderWithCatalogs(list, same)
	if _, err := strict.LoadTable(ctx, ref("delta")); err == nil || errors.Is(err, connections.ErrTableUnsupported) {
		t.Errorf("400 on a strict reader = %v, want a plain error", err)
	}
}

// TestListTables_Classifier: tables the classifier names come back marked
// unsupported; a failing classifier leaves the listing unmarked.
func TestListTables_Classifier(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/config":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/v1/namespaces/s/tables":
			_ = json.NewEncoder(w).Encode(map[string]any{"identifiers": []map[string]any{
				{"namespace": []string{"s"}, "name": "ok"},
				{"namespace": []string{"s"}, "name": "bad"},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	list := func(context.Context) ([]connections.CatalogRef, error) { return nil, nil }
	same := func(c string) string { return c }
	ns := connections.Namespace{"s"}
	reason := connections.Unsupported{Code: connections.UnsupportedKind, Detail: "K"}

	marks := func(context.Context, string, connections.Namespace) (map[string]connections.Unsupported, error) {
		return map[string]connections.Unsupported{"bad": reason}, nil
	}
	r := New(srv.URL, Auth{}, nil, srv.Client()).ReaderSkippingUnsupported(list, same, marks)
	tables, _, err := r.ListTables(context.Background(), "c", ns, "")
	if err != nil || len(tables) != 2 || tables[0].Unsupported.Is() || tables[1].Unsupported != reason {
		t.Fatalf("ListTables = %+v, %v; want ok readable and bad marked", tables, err)
	}

	broken := func(context.Context, string, connections.Namespace) (map[string]connections.Unsupported, error) {
		return nil, errors.New("boom")
	}
	r = New(srv.URL, Auth{}, nil, srv.Client()).ReaderSkippingUnsupported(list, same, broken)
	tables, _, err = r.ListTables(context.Background(), "c", ns, "")
	if err != nil || len(tables) != 2 || tables[0].Unsupported.Is() || tables[1].Unsupported.Is() {
		t.Fatalf("ListTables with a failing classifier = %+v, %v; want both unmarked", tables, err)
	}
}
