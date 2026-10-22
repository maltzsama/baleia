package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/assistant/llm"
	"github.com/galileostudio/baleia/internal/connections"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
)

// TestLLMProvider_GoesThroughTheDialer: a provider base URL on a denied
// address (loopback here) is refused by the connections dialer and the
// server is never reached (#378).
func TestLLMProvider_GoesThroughTheDialer(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true }))
	defer srv.Close()

	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h := testAPI(handlerFields{dialer: d})
	provider, model, base := "openai_compatible", "m", srv.URL
	p, err := h.llmProvider(adb.AssistantSetting{Provider: &provider, Model: &model, BaseUrl: &base}, "key")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Stream(context.Background(), llm.Request{Messages: []llm.Message{{Role: "user", Content: "hi"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "not an allowed destination") {
		t.Fatalf("stream to loopback: err = %v, want the dialer's refusal", err)
	}
	if reached {
		t.Fatal("the provider reached a denied address")
	}
}
