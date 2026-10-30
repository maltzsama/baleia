package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestNew_SigningRegion: a configured region signs every request and skips the
// bucket-location lookup that region-strict stores reject (#510); without one
// the client still discovers it, as AWS, MinIO and RustFS expect.
func TestNew_SigningRegion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		region         string
		expectedScope  string
		expectLocation bool
	}{
		{
			name:          "configured region",
			region:        "sa-saopaulo-1",
			expectedScope: "/sa-saopaulo-1/s3/aws4_request",
		},
		{
			name:           "discovered region",
			expectedScope:  "/us-east-1/s3/aws4_request",
			expectLocation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu       sync.Mutex
				auths    []string
				location bool
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Query().Has("location") {
					location = true
					w.Header().Set("Content-Type", "application/xml")
					_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`))
					return
				}
				auths = append(auths, r.Header.Get("Authorization"))
			}))
			t.Cleanup(srv.Close)

			c, err := New(Options{
				Endpoint:  srv.URL,
				AccessKey: "key",
				SecretKey: "secret",
				Bucket:    "results",
				Region:    tt.region,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := c.Health(context.Background()); err != nil {
				t.Fatalf("Health: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if location != tt.expectLocation {
				t.Errorf("bucket-location lookup = %v, want %v", location, tt.expectLocation)
			}
			if len(auths) == 0 {
				t.Fatal("no signed bucket request reached the server")
			}
			for _, a := range auths {
				if !strings.Contains(a, tt.expectedScope) {
					t.Errorf("Authorization %q does not sign for %q", a, tt.expectedScope)
				}
			}
		})
	}
}
