//go:build opa

package opa

import (
	"fmt"
	"slices"
	"testing"
)

// Q1. Composition: file rules + catalog guard + OPA keep every guarantee of
// ADR 0005 and ADR 0018. The same cases run on the coordinator without OPA
// and on the one with OPA (active bundle, no policies); each case must
// behave the same on both.
func TestComposition_GuardGuaranteesHoldWithOPA(t *testing.T) {
	publish(t, newBundle("q1"), "trino-opa")
	unscoped := as("u_q1")
	user := unscoped.with("baleia.catalogs", "tpch,memory,iceberg")
	readonly := user.with("baleia.mode", "readonly")
	scoped := unscoped.with("baleia.catalogs", "tpch")

	cases := []struct {
		name    string
		session session
		sql     string
		allowed bool
	}{
		{"admin creates a catalog", session{}, "CREATE CATALOG q1_tmp USING memory", true},
		{"admin drops a catalog", session{}, "DROP CATALOG q1_tmp", true},
		{"user cannot create a catalog", user, "CREATE CATALOG q1_evil USING memory", false},
		{"user cannot drop a catalog", user, "DROP CATALOG tpch", false},
		{"service identity cannot create a catalog", as(serviceUser), "CREATE CATALOG q1_evil USING memory", false},
		{"service identity reads", as(serviceUser), "SELECT count(*) FROM tpch.tiny.nation", true},
		{"another login cannot impersonate", session{login: outsider, user: "u_q1"}, "SELECT 1", false},
		{"another login cannot become the admin", session{login: outsider, user: adminUser}, "SELECT 1", false},
		{"user without a catalog scope reads nothing", unscoped, "SELECT count(*) FROM tpch.tiny.nation", false},
		{"user reads", user, "SELECT count(*) FROM tpch.tiny.nation", true},
		{"user writes", user, "CREATE TABLE IF NOT EXISTS memory.default.q1_rw AS SELECT 1 AS x", true},
		{"read-only session reads", readonly, "SELECT count(*) FROM tpch.tiny.nation", true},
		{"read-only session cannot create a table", readonly, "CREATE TABLE memory.default.q1_ro AS SELECT 1 AS x", false},
		{"read-only session cannot insert", readonly, "INSERT INTO memory.default.q1_rw VALUES (2)", false},
		{"read-only session cannot drop", readonly, "DROP TABLE memory.default.q1_rw", false},
		{"scoped session reads its catalog", scoped, "SELECT count(*) FROM tpch.tiny.nation", true},
		{"scoped session cannot read another catalog", scoped, "SELECT count(*) FROM memory.information_schema.tables", false},
	}

	variants := []string{"trino-baseline", "trino-opa"}
	outcomes := map[string][]string{}
	for _, variant := range variants {
		c := connect(t, "svc/"+variant)
		for _, tc := range cases {
			t.Run(variant+"/"+tc.name, func(t *testing.T) {
				res, err := c.run(t.Context(), tc.session, tc.sql)
				if err != nil {
					t.Fatal(err)
				}
				allowed := res.err == nil
				outcomes[variant] = append(outcomes[variant], fmt.Sprintf("%s=%t", tc.name, allowed))
				if allowed != tc.allowed {
					t.Errorf("%s: allowed=%t (%v), want %t", tc.sql, allowed, res.err, tc.allowed)
				}
			})
		}
		t.Run(variant+"/scoped session lists only its catalog", func(t *testing.T) {
			got := c.query(t, scoped, "SHOW CATALOGS").strings()
			if !slices.Equal(got, []string{"system", "tpch"}) && !slices.Equal(got, []string{"tpch"}) {
				t.Errorf("SHOW CATALOGS = %v, want tpch (and system) only", got)
			}
			outcomes[variant] = append(outcomes[variant], fmt.Sprint(got))
		})
	}
	if !slices.Equal(outcomes[variants[0]], outcomes[variants[1]]) {
		t.Errorf("outcomes differ:\n%s: %v\n%s: %v", variants[0], outcomes[variants[0]], variants[1], outcomes[variants[1]])
	}
}
