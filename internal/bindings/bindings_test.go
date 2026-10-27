package bindings_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/galileostudio/baleia/internal/bindings"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/connections"
)

func TestRegistryAndConnector(t *testing.T) {
	t.Parallel()
	b := bindtrino.New(slog.New(slog.DiscardHandler), nil, nil)
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
