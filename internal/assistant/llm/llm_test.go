package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
)

func TestHTTPProviderUsesConfiguredHTTPClient(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	dialer, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provider := NewHTTPProvider("openai_compatible", server.URL, "key", "model", dialer.HTTPClient(120*time.Second))
	_, err = provider.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}}, nil)
	if !errors.Is(err, connections.ErrDeniedNetwork) {
		t.Fatalf("want ErrDeniedNetwork, got %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("server received %d requests, want 0", got)
	}
}

func TestHTTPProviderWithoutClientFailsClosed(t *testing.T) {
	provider := NewHTTPProvider("openai_compatible", "https://example.invalid/v1", "key", "model", nil)
	_, err := provider.Stream(context.Background(), Request{}, nil)
	if err == nil || err.Error() != "assistant HTTP client is not configured" {
		t.Fatalf("want missing-client error, got %v", err)
	}
}
