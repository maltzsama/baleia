package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
)

// TestRegistrationIsOneTransaction (ADR 0029): the connection, its native
// engine, native catalogs and bindings commit together, and a failure at any
// write leaves none of them. It runs on every engine the test database can be.
func TestRegistrationIsOneTransaction(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	r := New(gdb)
	owner := PGUUID(string(createAPIKeyUser(t, gdb, "registration-owner@example.test").ID))

	register := func(connName, engineName string, natives []string, after error) (db.CatalogConnection, error) {
		var conn db.CatalogConnection
		err := r.TxCatalogWrite(ctx, func(q *Repo) error {
			var err error
			conn, err = q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
				ID: db.UUID{Bytes: uuid.New(), Valid: true}, Name: connName, Type: "clickhouse",
				AccessMode: strPtr("direct"), Url: "clickhouse://ch:9000", Config: []byte(`{}`), CreatedBy: owner,
			})
			if err != nil {
				return err
			}
			_, _, err = manage.CreateIn(ctx, q, manage.NewEngine{
				Name: engineName, Kind: "clickhouse", ConnectionID: conn.ID, CreatedBy: owner, Natives: natives,
			})
			if err != nil {
				return err
			}
			return after
		})
		return conn, err
	}
	connectionExists := func(name string) bool {
		rows, err := r.ListCatalogConnections(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range rows {
			if c.Name == name {
				return true
			}
		}
		return false
	}

	conn, err := register("first", "first", []string{"default", "logs"}, nil)
	if err != nil {
		t.Fatalf("a valid registration failed: %v", err)
	}
	cats, err := r.ListCatalogsByConnection(ctx, conn.ID)
	if err != nil || len(cats) != 2 {
		t.Fatalf("native catalogs = %d, %v; want 2", len(cats), err)
	}

	// The engine name is taken: the connection written first must roll back.
	if _, err := register("second", "first", []string{"default"}, nil); !errors.Is(manage.CreateError(err), manage.ErrNameTaken) {
		t.Fatalf("a taken engine name = %v, want ErrNameTaken", err)
	}
	if connectionExists("second") {
		t.Fatal("the connection of a failed registration was committed")
	}

	// A later write fails, after the connection, the engine, its catalogs and
	// their bindings were written: all of them roll back.
	later := errors.New("a later write failed")
	if _, err := register("third", "third", []string{"default"}, later); !errors.Is(err, later) {
		t.Fatalf("register = %v, want the later failure", err)
	}
	if connectionExists("third") {
		t.Fatal("the connection of a registration that failed late was committed")
	}
	engines, err := r.ListEngines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range engines {
		if e.Name == "third" {
			t.Fatal("the engine of a registration that failed late was committed")
		}
	}
}
