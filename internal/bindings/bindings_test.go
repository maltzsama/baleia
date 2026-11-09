package bindings_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/galileostudio/baleia/internal/bindings"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
)

func TestRegistryAndConnector(t *testing.T) {
	t.Parallel()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := providers.New(d)
	if err != nil {
		t.Fatal(err)
	}
	b := bindtrino.New(slog.New(slog.DiscardHandler), nil, nil).WithProviders(reg)
	bindings.Register(b)
	got, ok := bindings.For("trino")
	if !ok || got != b {
		t.Fatal("registered binder not found")
	}
	if _, ok := bindings.For("doris"); ok {
		t.Error("no doris binder yet (#21)")
	}
	if c, err := bindings.Connector(b, connections.KindPolaris, ""); err != nil || c != "iceberg" {
		t.Errorf("default connector = %q, %v", c, err)
	}
	if _, err := bindings.Connector(b, connections.KindPolaris, "hive"); !errors.Is(err, bindings.ErrUnsupportedSource) {
		t.Errorf("unsupported connector: %v", err)
	}
	if _, err := bindings.Connector(b, connections.KindSQLite, ""); !errors.Is(err, bindings.ErrUnsupportedSource) {
		t.Errorf("unbindable kind: %v", err)
	}
}
