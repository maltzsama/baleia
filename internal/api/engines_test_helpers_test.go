package api

import (
	"context"
	"testing"

	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
	qtrino "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5/pgtype"
)

// fixedEngines serves one engine for every lookup.
type fixedEngines struct{ e queryengine.Engine }

func (f fixedEngines) Get(context.Context, pgtype.UUID) (queryengine.Engine, error) { return f.e, nil }
func (f fixedEngines) DefaultTrino(context.Context) (queryengine.Engine, error)     { return f.e, nil }

// trinoBackends points the default engine and the catalog DDL at a (fake)
// coordinator, as the wiring site does with the real one.
func trinoBackends(url string) (engineSource, *bindtrino.Binder) {
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		panic(err)
	}
	eng := queryengine.Guard(qtrino.New("test-trino", url, "baleia", "baleia_admin", d, dialect.NewRegistry().MustGet(dialect.Trino)), false)
	return fixedEngines{eng}, bindtrino.New(trino.NewWithHTTPClient(url, "baleia_admin", d.HTTPClient(0)), nil)
}

// testEngines is a Trino engine whose coordinator is never reached.
func testEngines() engineSource {
	e, _ := trinoBackends("http://127.0.0.1:1")
	return e
}

// registryKinds adapts a kind registry to engineKinds, dialing through d, as
// the engine manager does in production.
type registryKinds struct {
	r *queryengine.Registry
	d *connections.Dialer
}

func (k registryKinds) HasKind(kind string) bool { return k.r.Has(kind) }

func (k registryKinds) Transient(ctx context.Context, kind string, cfg connections.Config) (queryengine.Engine, error) {
	return k.r.Transient(ctx, kind, cfg, k.d)
}

// testKinds knows the fake and Trino kinds, the adapters the api tests link
// (before #364 their init() registered them as a side effect of the import).
func testKinds(t *testing.T, d *connections.Dialer) engineKinds {
	t.Helper()
	r, err := queryengine.NewRegistry(fake.Spec(), qtrino.Spec(dialect.NewRegistry().MustGet(dialect.Trino)))
	if err != nil {
		t.Fatal(err)
	}
	return registryKinds{r: r, d: d}
}

// directTx runs fn on q without a transaction: the fixtures' fake queriers
// have no database to open one on.
type directTx struct{ q db.Querier }

func (t directTx) InTx(_ context.Context, fn func(db.Querier) error) error { return fn(t.q) }
