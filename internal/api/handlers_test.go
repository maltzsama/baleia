package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/audit"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/galileostudio/baleia/internal/wsticket"
)

// testDeps is a complete Deps over a throwaway database.
func testDeps(t *testing.T) Deps {
	t.Helper()
	pool := testdb.New(t)
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s3, err := storage.New("localhost:9000", "", "", "results", false)
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		Data:     DataDeps{Queries: db.New(pool), Pool: pool},
		Engines:  EngineDeps{Connections: testRegistry(t), Engines: testEngines(), Kinds: testKinds(t, nil), Dialects: dialect.NewRegistry(), Binder: bindtrino.New(nil, nil), Dialer: d},
		Settings: SettingsFromConfig(&config.Config{}),
		Temporal: &fakeTemporalClient{},
		Storage:  s3,
	}
}

// TestNewHandlers_RequiresDependencies: a missing dependency is an error at
// construction, naming what is missing, not a nil found at request time.
func TestNewHandlers_RequiresDependencies(t *testing.T) {
	_, err := NewHandlers(Deps{})
	if err == nil {
		t.Fatal("NewHandlers with missing dependencies returned no error")
	}
	for _, want := range []string{"Data.Pool", "Data.Queries", "Engines.Binder", "Engines.Dialer", "Storage", "Temporal"} {
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

// TestNewHandlers_Observability: the audit recorder, the metrics and the
// ticket store given in Deps are the ones the handlers use.
func TestNewHandlers_Observability(t *testing.T) {
	deps := testDeps(t)
	rec := &memAudit{}
	tickets := wsticket.New()
	deps.Observability = ObservabilityDeps{Audit: rec, Metrics: metrics.New("api")}
	deps.WSTickets = tickets
	h, err := NewHandlers(deps)
	if err != nil {
		t.Fatal(err)
	}
	if h.audit != rec || h.wsTickets != tickets {
		t.Error("the injected audit recorder and ticket store must be used")
	}
	if h.AuthMetrics() == nil {
		t.Error("metrics must be registered when a registry is given")
	}
}

// assertGroupsWired: every handler group embedded in Handlers is built and
// shares the one core (#365); a nil group would only fail at request time.
func assertGroupsWired(t *testing.T, h *Handlers) {
	t.Helper()
	v := reflect.ValueOf(h).Elem()
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if !f.Anonymous || f.Name == "core" {
			continue
		}
		g := v.Field(i)
		if g.IsNil() {
			t.Errorf("group %s is not built", f.Name)
			continue
		}
		if c := g.Elem().FieldByName("core"); !c.IsValid() || c.Pointer() != reflect.ValueOf(h.core).Pointer() {
			t.Errorf("group %s does not share the handlers' core", f.Name)
		}
	}
}

func TestNewHandlers_GroupsShareCore(t *testing.T) {
	h, err := NewHandlers(testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	assertGroupsWired(t, h)
	assertGroupsWired(t, testHandlers(handlerFields{}))
}
