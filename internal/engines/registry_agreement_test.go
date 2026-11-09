package engines

import (
	"slices"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/dialect"
)

// TestDirectAccessAgreesWithEngineRegistry is the agreement check between the
// connection registry and the native engine registry (ADR 0021 CR-14): a
// connection type may declare the direct access mode only when the engine kind
// it names exists. A direct type without an engine, or with a kind the engine
// registry does not provide, is a contract break, not a silent no-op.
func TestDirectAccessAgreesWithEngineRegistry(t *testing.T) {
	t.Parallel()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provReg, err := providers.New(d)
	if err != nil {
		t.Fatal(err)
	}
	kinds, err := NewRegistry(Options{Dialects: dialect.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range provReg.All() {
		if !slices.Contains(p.Meta().AccessModes, connections.AccessModeDirect) {
			continue
		}
		if p.EngineKind() == "" {
			t.Errorf("%s declares direct access but no engine kind", p.Kind())
			continue
		}
		if !kinds.Has(p.EngineKind()) {
			t.Errorf("%s declares direct access through engine %q, which the engine registry does not provide", p.Kind(), p.EngineKind())
		}
	}
}
