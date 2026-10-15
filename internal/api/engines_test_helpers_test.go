package api

import (
	"context"

	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	qtrino "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5/pgtype"
)

// fixedEngines serves one engine for every lookup.
type fixedEngines struct{ e queryengine.Engine }

func (f fixedEngines) Get(context.Context, pgtype.UUID) (queryengine.Engine, error) { return f.e, nil }
func (f fixedEngines) DefaultTrino(context.Context) (queryengine.Engine, error)    { return f.e, nil }

// trinoBackends points the default engine and the catalog DDL at a (fake)
// coordinator, as the wiring site does with the real one.
func trinoBackends(url string) (engineSource, *bindtrino.Binder) {
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		panic(err)
	}
	eng := queryengine.Guard(qtrino.New("test-trino", url, "baleia", "baleia_admin", d), false)
	return fixedEngines{eng}, bindtrino.New(trino.NewWithHTTPClient(url, "baleia_admin", d.HTTPClient(0)), nil)
}

// testEngines is a Trino engine whose coordinator is never reached.
func testEngines() engineSource {
	e, _ := trinoBackends("http://127.0.0.1:1")
	return e
}
