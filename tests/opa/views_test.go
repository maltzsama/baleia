//go:build opa

package opa

import (
	"slices"
	"strings"
	"testing"
)

// Q9. Views. Trino 484 evaluates a SECURITY DEFINER view as its owner (the
// owner's identity, without the caller's extra credentials) and a SECURITY
// INVOKER view as the caller. Each case runs on the coordinator without OPA
// too, so a denial can be told apart from a policy decision.
func TestViews_DefinerAndInvoker(t *testing.T) {
	for _, variant := range []string{"trino-baseline", "trino-opa"} {
		t.Run(variant, func(t *testing.T) {
			c := connect(t, "svc/"+variant)
			table := seedOrders(t, c, "orders_views_"+strings.ReplaceAll(variant, "-", "_"))
			short := table[strings.LastIndex(table, ".")+1:]
			if variant == "trino-opa" {
				publish(t, ordersPolicies("q9", short), variant)
			}
			views := map[string]struct {
				owner    session
				security string
			}{
				"v_definer_user":  {user("u_ana"), "DEFINER"},
				"v_invoker_user":  {user("u_ana"), "INVOKER"},
				"v_definer_admin": {admin, "DEFINER"},
			}
			for name, v := range views {
				c.query(t, admin, "DROP VIEW IF EXISTS memory.default."+name)
				c.query(t, v.owner, "CREATE VIEW memory.default."+name+" SECURITY "+v.security+
					" AS SELECT id, region, email FROM "+table)
			}
			read := func(view string) (*result, error) {
				return c.run(t.Context(), user("u_bob"), "SELECT id, region, email FROM memory.default."+view+" ORDER BY id")
			}

			t.Run("definer view of an ordinary user is denied by the catalog guard", func(t *testing.T) {
				// The owner's identity carries no baleia.catalogs, so the guard's
				// fail-closed scope (ADR 0018) refuses it, with or without OPA.
				res, err := read("v_definer_user")
				if err != nil {
					t.Fatal(err)
				}
				if res.err == nil || !strings.Contains(res.err.Message, "Cannot access catalog iceberg") {
					t.Fatalf("got rows %q, error %v; want the guard's catalog denial", res.strings(), res.err)
				}
			})
			t.Run("invoker view applies the caller's policy", func(t *testing.T) {
				res, err := read("v_invoker_user")
				if err != nil {
					t.Fatal(err)
				}
				if res.err != nil {
					t.Fatal(res.err)
				}
				expected := []string{"1|BR|ana@example.com", "2|US|bob@example.com", "3|BR|cid@example.com", "4|EU|dan@example.com"}
				if variant == "trino-opa" {
					expected = []string{"2|US|***"}
				}
				if got := res.strings(); !slices.Equal(got, expected) {
					t.Errorf("rows = %q, want %q", got, expected)
				}
			})
			t.Run("definer view of a trusted owner is unfiltered", func(t *testing.T) {
				// Why publishing a policy must refuse trusted-owned DEFINER views
				// over a policied table (handover decision 5).
				res, err := read("v_definer_admin")
				if err != nil {
					t.Fatal(err)
				}
				if res.err != nil {
					t.Fatal(res.err)
				}
				expected := []string{"1|BR|ana@example.com", "2|US|bob@example.com", "3|BR|cid@example.com", "4|EU|dan@example.com"}
				if got := res.strings(); !slices.Equal(got, expected) {
					t.Errorf("rows = %q, want %q", got, expected)
				}
			})
		})
	}
}
