package api

import (
	"testing"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/dialect"
)

// TestNewHandlers_Defaults characterizes what the constructor fills in on its
// own: a no-op audit recorder, the caches, the content service and a ticket
// store. The assistant service needs a pool.
func TestNewHandlers_Defaults(t *testing.T) {
	h := NewHandlers(nil, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, dialect.NewRegistry(), nil)
	if _, ok := h.audit.(audit.Nop); !ok {
		t.Errorf("audit = %T, want audit.Nop", h.audit)
	}
	if h.catalogCache == nil || h.versionCache == nil {
		t.Error("caches must be built")
	}
	if h.content == nil {
		t.Error("content service must be built")
	}
	if h.wsTickets == nil {
		t.Error("ticket store must be built")
	}
	if h.assistant != nil {
		t.Error("no assistant service without a pool")
	}
}
