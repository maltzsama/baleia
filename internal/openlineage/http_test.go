package openlineage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPPublisher(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom/events" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		var event RunEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil || event.Job.Name != "job" {
			t.Errorf("invalid event: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	publisher := HTTPPublisher{Client: server.Client(), Endpoint: server.URL + "/custom/events", BearerToken: "secret"}
	if err := publisher.Publish(context.Background(), RunEvent{Job: Job{Name: "job"}}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPPublisherRetriesTransientFailure(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	publisher := HTTPPublisher{Client: server.Client(), Endpoint: server.URL}
	if err := publisher.Publish(context.Background(), RunEvent{}); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestHTTPPublisherDoesNotRetryClientErrorOrLeakBody(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "sensitive details", http.StatusForbidden)
	}))
	defer server.Close()
	publisher := HTTPPublisher{Client: server.Client(), Endpoint: server.URL}
	err := publisher.Publish(context.Background(), RunEvent{})
	if err == nil || strings.Contains(err.Error(), "sensitive") || attempts.Load() != 1 {
		t.Fatalf("err = %v, attempts = %d", err, attempts.Load())
	}
}

func TestHTTPPublisherRejectsRedirect(t *testing.T) {
	t.Parallel()
	var targetCalled atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalled.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	publisher := HTTPPublisher{Client: redirect.Client(), Endpoint: redirect.URL}
	if err := publisher.Publish(context.Background(), RunEvent{}); err == nil {
		t.Fatal("redirect accepted")
	}
	if targetCalled.Load() {
		t.Fatal("redirect target was called")
	}
}

func TestValidateBearerTransport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		endpoint string
		token    bool
		allowed  bool
	}{
		{"http://example.test/events", true, false},
		{"http://localhost/events", true, false},
		{"http://127.0.0.1/events", true, true},
		{"http://[::1]/events", true, true},
		{"https://example.test/events", true, true},
		{"http://example.test/events", false, true},
	} {
		if got := ValidateBearerTransport(tc.endpoint, tc.token) == nil; got != tc.allowed {
			t.Errorf("%s token=%v: allowed=%v", tc.endpoint, tc.token, got)
		}
	}
}

func TestHTTPPublisherSendsBearerOverTLS(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing bearer token")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	publisher := HTTPPublisher{Client: server.Client(), Endpoint: server.URL, BearerToken: "secret"}
	if err := publisher.Publish(context.Background(), RunEvent{}); err != nil {
		t.Fatal(err)
	}
}
