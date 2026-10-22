package api

import (
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/audit"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/testdb"
)

// testDeps is a complete Deps over a throwaway database.
func testDeps(t *testing.T) Deps {
	t.Helper()
	pool := testdb.New(t)
	s3, err := storage.New("localhost:9000", "", "", "results", false)
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		Data:     DataDeps{Queries: db.New(pool), Pool: pool},
		Engines:  EngineDeps{Connections: testRegistry(t), Engines: testEngines(), Kinds: testKinds(t, nil), Dialects: dialect.NewRegistry(), Binder: bindtrino.New(nil, nil)},
		Config:   &config.Config{},
		Temporal: &fakeTemporalClient{},
		Storage:  s3,
	}
}

// TestNewHandlers_RequiresDependencies: a missing dependency is an error at
// construction, naming what is missing, not a nil found at request time.
func TestNewHandlers_RequiresDependencies(t *testing.T) {
	_, err := NewHandlers(Deps{Config: &config.Config{}})
	if err == nil {
		t.Fatal("NewHandlers with missing dependencies returned no error")
	}
	for _, want := range []string{"Data.Pool", "Data.Queries", "Engines.Binder", "Storage", "Temporal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// TestNewHandlers_Defaults characterizes what the constructor fills in on its
// own: a no-op audit recorder, the caches, the content and assistant services
// and a ticket store.
func TestNewHandlers_Defaults(t *testing.T) {
	h, err := NewHandlers(testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.audit.(audit.Nop); !ok {
		t.Errorf("audit = %T, want audit.Nop", h.audit)
	}
	if h.catalogCache == nil || h.versionCache == nil {
		t.Error("caches must be built")
	}
	if h.content == nil || h.assistant == nil {
		t.Error("content and assistant services must be built")
	}
	if h.wsTickets == nil {
		t.Error("ticket store must be built")
	}
}
