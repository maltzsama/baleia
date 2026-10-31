package api

import (
	"log/slog"
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
	s3, err := storage.New(storage.Options{Endpoint: "localhost:9000", Bucket: "results"})
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		Data:     DataDeps{Queries: db.New(pool), Pool: pool},
		Engines:  EngineDeps{Connections: testRegistry(t), Engines: testEngines(), Kinds: testKinds(t, nil), Dialects: dialect.NewRegistry(), Binder: bindtrino.New(slog.New(slog.DiscardHandler), nil, nil), Dialer: d},
		Settings: SettingsFromConfig(&config.Config{}),
		Temporal: &fakeTemporalClient{},
		Storage:  s3,
	}
}

// TestNewAPI_RequiresDependencies: a missing dependency is an error at
// construction, naming what is missing, not a nil found at request time.
func TestNewAPI_RequiresDependencies(t *testing.T) {
	t.Parallel()
	_, err := NewAPI(Deps{})
	if err == nil {
		t.Fatal("NewAPI with missing dependencies returned no error")
	}
	for _, want := range []string{"Data.Pool", "Data.Queries", "Engines.Binder", "Engines.Dialer", "Storage", "Temporal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// TestNewAPI_Defaults characterizes what the constructor fills in on its
// own: a no-op audit recorder, the caches, the content and assistant services
// and a ticket store.
func TestNewAPI_Defaults(t *testing.T) {
	t.Parallel()
	h, err := NewAPI(testDeps(t))
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

// TestNewAPI_Observability: the audit recorder, the metrics and the
// ticket store given in Deps are the ones the handlers use.
func TestNewAPI_Observability(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	rec := &memAudit{}
	tickets := wsticket.New()
	deps.Observability = ObservabilityDeps{Audit: rec, Metrics: metrics.New("api")}
	deps.WSTickets = tickets
	h, err := NewAPI(deps)
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

// assertGroupsWired: every handler group embedded in API is built and
// shares the one core (#365); a nil group would only fail at request time.
func assertGroupsWired(t *testing.T, h *API) {
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

func TestNewAPI_GroupsShareCore(t *testing.T) {
	t.Parallel()
	h, err := NewAPI(testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	assertGroupsWired(t, h)
	assertGroupsWired(t, testAPI(handlerFields{}))
}

// TestNewAuthHandlers_RealSeams: outside tests the auth seams reach the real
// directory and the network.
func TestNewAuthHandlers_RealSeams(t *testing.T) {
	t.Parallel()
	a := newAuthHandlers(&core{})
	if a.logins == nil || a.sessions == nil || a.resets == nil || a.setup == nil {
		t.Error("the identity services must be built")
	}
	if _, ok := a.prober.(networkProber); !ok {
		t.Errorf("prober = %T, want networkProber", a.prober)
	}
}

// TestNewOpsHandlers_RealSeams: the status probes reach Temporal and the
// engine manager; without a Temporal client there is no schedule probe.
func TestNewOpsHandlers_RealSeams(t *testing.T) {
	t.Parallel()
	o := newOpsHandlers(&core{}, nil)
	if o.schedules != nil {
		t.Errorf("schedules = %T, want nil without a Temporal client", o.schedules)
	}
	if _, ok := o.engineStatus.(managedEngines); !ok {
		t.Errorf("engineStatus = %T, want managedEngines", o.engineStatus)
	}
	o = newOpsHandlers(&core{temporalClient: &fakeTemporalClient{}}, nil)
	if _, ok := o.schedules.(temporalSchedules); !ok {
		t.Errorf("schedules = %T, want temporalSchedules", o.schedules)
	}
}
