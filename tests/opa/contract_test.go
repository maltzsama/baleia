//go:build opa

package opa

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var admin = session{}

// scopeAll is the catalog scope Baleia sends for a user of a workspace bound
// to every spike catalog.
const scopeAll = "tpch,memory,iceberg"

func user(name string) session { return as(name).with("baleia.catalogs", scopeAll) }

// seedOrders recreates iceberg.spike.<table> with four orders.
func seedOrders(t testing.TB, c *trino, table string) string {
	t.Helper()
	name := "iceberg.spike." + table
	c.query(t, admin, "CREATE SCHEMA IF NOT EXISTS iceberg.spike")
	c.query(t, admin, "DROP TABLE IF EXISTS "+name)
	c.query(t, admin, "CREATE TABLE "+name+" (id integer, region varchar, status varchar, email varchar)")
	c.query(t, admin, "INSERT INTO "+name+" VALUES "+
		"(1, 'BR', 'open', 'ana@example.com'), (2, 'US', 'open', 'bob@example.com'), "+
		"(3, 'BR', 'void', 'cid@example.com'), (4, 'EU', 'open', 'dan@example.com')")
	return name
}

// ordersPolicies is the PostgreSQL-style example: teams see their region,
// nobody but auditors sees void orders, e-mails are masked for everyone and
// partially shown to analysts.
func ordersPolicies(revision, table string) *dataBundle {
	b := newBundle(revision)
	b.Subjects["u_ana"] = subject{User: "ana", Teams: []string{"br"}, Groups: []string{"analysts"}}
	b.Subjects["u_bob"] = subject{User: "bob", Teams: []string{"us"}, Groups: []string{}}
	b.Subjects["u_cid"] = subject{User: "cid", Teams: []string{"br", "us"}, Groups: []string{"auditors"}}
	p := b.table("iceberg", "spike", table)
	p.RowPolicies = []rowPolicy{
		{Name: "br", Mode: "permissive", To: []subjectRef{{Team: "br"}}, Using: "region = 'BR'"},
		{Name: "us", Mode: "permissive", To: []subjectRef{{Team: "us"}}, Using: "region = 'US'"},
		{Name: "no-void", Mode: "restrictive", To: []subjectRef{{Everyone: true, Except: []subjectRef{{Group: "auditors"}}}}, Using: "status <> 'void'"},
	}
	p.ColumnMasks["email"] = []columnMask{
		{Name: "hide", Priority: 1, To: []subjectRef{{Everyone: true}}, Expression: "'***'"},
		{Name: "analysts", Priority: 5, To: []subjectRef{{Group: "analysts"}}, Expression: "substr(email, 1, 2) || '***'"},
		{Name: "auditors", Priority: 9, To: []subjectRef{{Group: "auditors"}}, Expression: "email"},
	}
	return b
}

func selectOrders(t testing.TB, c *trino, s session, table string) []string {
	t.Helper()
	return c.query(t, s, "SELECT id, region, status, email FROM "+table+" ORDER BY id").strings()
}

// Q2. Reads: the filter and mask each identity gets, with the batched
// endpoints (trino-opa) and without them (trino-opa-single).
func TestContract_FiltersAndMasksOnSelect(t *testing.T) {
	for _, variant := range []string{"trino-opa", "trino-opa-single"} {
		t.Run(variant, func(t *testing.T) {
			c := connect(t, "svc/"+variant)
			table := seedOrders(t, c, "orders_read_"+strings.ReplaceAll(variant, "-", "_"))
			short := table[strings.LastIndex(table, ".")+1:]
			publish(t, ordersPolicies("q2-read-"+variant, short), variant)

			cases := []struct {
				name     string
				session  session
				expected []string
			}{
				{"team br analyst", user("u_ana"), []string{"1|BR|open|an***"}},
				{"team us", user("u_bob"), []string{"2|US|open|***"}},
				{"auditor in both teams", user("u_cid"), []string{
					"1|BR|open|ana@example.com", "2|US|open|bob@example.com", "3|BR|void|cid@example.com"}},
				{"unknown identity is default-deny", user("u_stranger"), []string{}},
				{"admin is unfiltered", admin, []string{
					"1|BR|open|ana@example.com", "2|US|open|bob@example.com",
					"3|BR|void|cid@example.com", "4|EU|open|dan@example.com"}},
				{"service identity is unfiltered", as(serviceUser), []string{
					"1|BR|open|ana@example.com", "2|US|open|bob@example.com",
					"3|BR|void|cid@example.com", "4|EU|open|dan@example.com"}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					got := selectOrders(t, c, tc.session, table)
					if !slices.Equal(got, tc.expected) {
						t.Errorf("rows = %q, want %q", got, tc.expected)
					}
				})
			}
			t.Run("filter applies inside aggregates and joins", func(t *testing.T) {
				got := c.query(t, user("u_bob"), "SELECT count(*), count(DISTINCT a.email) FROM "+table+" a JOIN "+table+" b ON a.id = b.id").strings()
				if !slices.Equal(got, []string{"1|1"}) {
					t.Errorf("count = %q, want 1|1", got)
				}
			})
			t.Run("a masked column cannot be read through a predicate", func(t *testing.T) {
				// The mask applies before the user's WHERE: filtering on the
				// real value finds nothing.
				got := c.query(t, user("u_bob"), "SELECT id FROM "+table+" WHERE email = 'bob@example.com'").strings()
				if len(got) != 0 {
					t.Errorf("rows = %q, want none", got)
				}
			})
		})
	}
}

// Q2. Writes. For each write statement and each kind of policy (row filter
// only, column mask only) Trino's outcome is fixed below, and the invariant
// that matters is checked: rows the identity cannot see never change.
func TestContract_Writes(t *testing.T) {
	c := connect(t, "svc/trino-opa")
	rowsOnly := ordersPolicies("", "orders_w_rows")
	rowsOnly.table("iceberg", "spike", "orders_w_rows").ColumnMasks = nil
	masksOnly := ordersPolicies("", "orders_w_masks")
	masksOnly.table("iceberg", "spike", "orders_w_masks").RowPolicies = nil

	statements := []struct {
		name string
		sql  string
	}{
		{"insert inside the filter", "INSERT INTO %s VALUES (8, 'US', 'open', 'new@example.com')"},
		{"insert outside the filter", "INSERT INTO %s VALUES (9, 'BR', 'open', 'new@example.com')"},
		{"update", "UPDATE %s SET status = 'shipped'"},
		{"update moving a row out of the filter", "UPDATE %s SET region = 'EU' WHERE id = 2"},
		{"delete", "DELETE FROM %s"},
		{"merge", "MERGE INTO %s t USING (VALUES 1, 2, 3, 4) s(id) ON t.id = s.id WHEN MATCHED THEN UPDATE SET status = 'merged'"},
	}
	// Trino 484's answers. With a row filter, INSERT is checked against the
	// filter (PostgreSQL's WITH CHECK), DELETE removes only visible rows, and
	// UPDATE and MERGE are refused. With a column mask every write is refused.
	expected := map[string]map[string]string{
		"rows": {
			"insert inside the filter":              "OK",
			"insert outside the filter":             "PERMISSION_DENIED",
			"update":                                "NOT_SUPPORTED",
			"update moving a row out of the filter": "NOT_SUPPORTED",
			"delete":                                "OK",
			"merge":                                 "NOT_SUPPORTED",
		},
		"masks": {
			"insert inside the filter":              "NOT_SUPPORTED",
			"insert outside the filter":             "NOT_SUPPORTED",
			"update":                                "NOT_SUPPORTED",
			"update moving a row out of the filter": "NOT_SUPPORTED",
			"delete":                                "NOT_SUPPORTED",
			"merge":                                 "NOT_SUPPORTED",
		},
	}
	for _, kind := range []string{"rows", "masks"} {
		bundle := rowsOnly
		if kind == "masks" {
			bundle = masksOnly
		}
		for _, st := range statements {
			t.Run(kind+"/"+st.name, func(t *testing.T) {
				short := "orders_w_" + kind
				table := seedOrders(t, c, short)
				bundle.Revision = fmt.Sprintf("q2-w-%s-%d", kind, time.Now().UnixNano())
				publish(t, bundle, "trino-opa")
				before := selectOrders(t, c, admin, table)
				res, err := c.run(t.Context(), user("u_bob"), fmt.Sprintf(st.sql, table))
				if err != nil {
					t.Fatal(err)
				}
				after := selectOrders(t, c, admin, table)
				outcome := "OK"
				if res.err != nil {
					outcome = res.err.ErrorName
				}
				t.Logf("%-6s %-38s -> %s %v\n  after: %q", kind, st.name, outcome, res.err, after)
				if want := expected[kind][st.name]; outcome != want {
					t.Errorf("outcome %s, want %s", outcome, want)
				}
				if kind == "rows" {
					// u_bob sees only id 2 (team us, not void).
					for _, row := range before {
						if strings.HasPrefix(row, "2|") {
							continue
						}
						if !slices.Contains(after, row) {
							t.Errorf("invisible row %q changed or disappeared: %q", row, after)
						}
					}
				}
			})
		}
	}
}

// Q2. Two masks with the same priority on one column cannot be resolved: the
// policy conflicts and Trino fails the query instead of picking one.
func TestContract_ConflictingMasksFailTheQuery(t *testing.T) {
	for _, variant := range []string{"trino-opa", "trino-opa-single"} {
		t.Run(variant, func(t *testing.T) {
			c := connect(t, "svc/"+variant)
			table := seedOrders(t, c, "orders_conflict_"+strings.ReplaceAll(variant, "-", "_"))
			short := table[strings.LastIndex(table, ".")+1:]
			b := ordersPolicies("q2-conflict-"+variant, short)
			b.table("iceberg", "spike", short).ColumnMasks["email"] = []columnMask{
				{Name: "a", Priority: 1, To: []subjectRef{{Everyone: true}}, Expression: "'a'"},
				{Name: "b", Priority: 1, To: []subjectRef{{Everyone: true}}, Expression: "'b'"},
			}
			publish(t, b, variant)
			qerr := c.denied(t, user("u_ana"), "SELECT email FROM "+table)
			t.Logf("conflicting masks: %v", qerr)
			// Trino asks for the masks of every column of the table, so the
			// conflict fails every read of it, not only of that column.
			c.denied(t, user("u_ana"), "SELECT id FROM "+table)
		})
	}
}

// Q2. The real plugin input: every operation Trino sends during the suite's
// statements, per endpoint. With OPA_SPIKE_RECORD=1 one example per
// (endpoint, operation) is written to deploy/opa/baleia/testdata/trino484,
// which the Rego tests evaluate.
func TestContract_CapturedInputs(t *testing.T) {
	start := time.Now()
	c := connect(t, "svc/trino-opa")
	single := connect(t, "svc/trino-opa-single")
	table := seedOrders(t, c, "orders_contract")
	b := ordersPolicies("q2-contract", "orders_contract")
	publish(t, b, "trino-opa", "trino-opa-single")
	u := user("u_ana")
	statements := []string{
		"SHOW CATALOGS",
		"SHOW SCHEMAS FROM iceberg",
		"SHOW TABLES FROM iceberg.spike",
		"SHOW COLUMNS FROM " + table,
		"SELECT id, email FROM " + table,
		"SELECT * FROM iceberg.information_schema.columns WHERE table_name = 'orders_contract'",
		"INSERT INTO " + table + " VALUES (7, 'BR', 'open', 'x@example.com')",
		"UPDATE " + table + " SET status = 'x' WHERE id = 7",
		"DELETE FROM " + table + " WHERE id = 7",
		"MERGE INTO " + table + " t USING (VALUES 1) s(id) ON t.id = s.id WHEN MATCHED THEN UPDATE SET status = 'open'",
		"DROP TABLE IF EXISTS iceberg.spike.orders_contract_ctas",
		"CREATE TABLE iceberg.spike.orders_contract_ctas AS SELECT id FROM " + table,
		"DROP TABLE iceberg.spike.orders_contract_ctas",
		"CREATE VIEW memory.default.orders_contract_v AS SELECT id FROM " + table,
		"SELECT * FROM memory.default.orders_contract_v",
		"DROP VIEW memory.default.orders_contract_v",
		"EXPLAIN SELECT * FROM " + table,
		"SELECT * FROM system.runtime.queries LIMIT 1",
		"SET SESSION query_max_run_time = '1h'",
	}
	for _, sql := range statements {
		for _, cl := range []*trino{c, single} {
			if res, err := cl.run(t.Context(), u, sql); err != nil {
				t.Fatalf("%s: %v", sql, err)
			} else if res.err != nil {
				t.Logf("%s: %v", sql, res.err)
			}
		}
	}

	type key struct{ variant, path, operation string }
	examples := map[key]decision{}
	for _, variant := range []string{"trino-opa", "trino-opa-single"} {
		for _, d := range decisionsSince(t, variant, start) {
			if d.user() != "u_ana" {
				continue
			}
			k := key{variant, d.Path, d.operation()}
			// Prefer an example about the test's own table over metadata ones.
			prev, seen := examples[k]
			if !seen || (!mentions(prev, "orders_contract") && mentions(d, "orders_contract")) {
				examples[k] = d
			}
		}
	}
	var lines []string
	for _, k := range slices.SortedFunc(maps.Keys(examples), func(a, b key) int {
		return strings.Compare(a.variant+a.path+a.operation, b.variant+b.path+b.operation)
	}) {
		lines = append(lines, fmt.Sprintf("%-17s %-30s %s", k.variant, k.path, k.operation))
	}
	t.Logf("operations seen (%d):\n%s", len(lines), strings.Join(lines, "\n"))

	for _, required := range []key{
		{"trino-opa", "baleia/trino/allow", "ExecuteQuery"},
		{"trino-opa", "baleia/trino/allow", "SelectFromColumns"},
		{"trino-opa", "baleia/trino/batch", "FilterCatalogs"},
		{"trino-opa", "baleia/trino/row_filters", "GetRowFilters"},
		{"trino-opa", "baleia/trino/batch_column_masks", "GetColumnMask"},
		{"trino-opa-single", "baleia/trino/column_mask", "GetColumnMask"},
		{"trino-opa", "baleia/trino/allow", "UpdateTableColumns"},
		{"trino-opa", "baleia/trino/allow", "DeleteFromTable"},
		{"trino-opa", "baleia/trino/allow", "InsertIntoTable"},
	} {
		if _, ok := examples[required]; !ok {
			t.Errorf("no decision for %v", required)
		}
	}

	if !envFlag("OPA_SPIKE_RECORD") {
		return
	}
	// opa test loads a JSON file at its directory's path, so each example
	// gets a directory: data.testdata.trino484.<name> = {rule, input, result}.
	dir := filepath.Join(repoRoot(), "deploy", "opa", "baleia", "testdata")
	if err := os.RemoveAll(filepath.Join(dir, "trino484")); err != nil {
		t.Fatal(err)
	}
	writeJSON := func(path string, v any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(filepath.Join(dir, "bundle", "data.json"), b)
	for k, d := range examples {
		rule := strings.TrimPrefix(k.path, "baleia/trino/")
		name := rule + "_" + k.operation
		if k.variant == "trino-opa-single" {
			name += "_single"
		}
		redactQueryID(d.Input)
		writeJSON(filepath.Join(dir, "trino484", name, "data.json"), map[string]any{"rule": rule, "input": d.Input, "result": d.Result})
	}
}

func mentions(d decision, text string) bool {
	raw, err := json.Marshal(d.Input)
	return err == nil && strings.Contains(string(raw), text)
}

// redactQueryID keeps recorded inputs stable across runs.
func redactQueryID(input map[string]any) {
	if ctx, ok := input["context"].(map[string]any); ok {
		if _, ok := ctx["queryId"]; ok {
			ctx["queryId"] = "20260101_000000_00000_xxxxx"
		}
	}
}

// Q2. Side doors inside Trino: Iceberg metadata tables such as <table>$files
// and $partitions expose a policied table's column bounds and values under
// another name, where no filter or mask applies. The Rego denies those to
// user identities; the structural ones stay readable and time travel stays
// filtered.
func TestContract_IcebergSideDoors(t *testing.T) {
	c := connect(t, "svc/trino-opa")
	table := seedOrders(t, c, "orders_side")
	publish(t, ordersPolicies("q2-side", "orders_side"), "trino-opa")
	snapshot := c.query(t, admin, `SELECT CAST(snapshot_id AS varchar) FROM iceberg.spike."orders_side$snapshots" ORDER BY committed_at DESC LIMIT 1`).strings()[0]
	for _, suffix := range []string{"files", "entries", "all_files", "all_entries", "partitions", "manifests", "all_manifests"} {
		t.Run("$"+suffix+" is denied", func(t *testing.T) {
			qerr := c.denied(t, user("u_bob"), `SELECT * FROM iceberg.spike."orders_side$`+suffix+`"`)
			if qerr.ErrorName != "PERMISSION_DENIED" {
				t.Errorf("error %v, want PERMISSION_DENIED", qerr)
			}
			c.query(t, admin, `SELECT count(*) FROM iceberg.spike."orders_side$`+suffix+`"`)
		})
	}
	for _, suffix := range []string{"history", "refs", "snapshots", "properties", "metadata_log_entries"} {
		t.Run("$"+suffix+" stays readable", func(t *testing.T) {
			c.query(t, user("u_bob"), `SELECT count(*) FROM iceberg.spike."orders_side$`+suffix+`"`)
		})
	}
	t.Run("time travel is filtered", func(t *testing.T) {
		got := c.query(t, user("u_bob"), "SELECT id, email FROM "+table+" FOR VERSION AS OF "+snapshot).strings()
		if !slices.Equal(got, []string{"2|***"}) {
			t.Errorf("rows = %q, want 2|***", got)
		}
	})
	t.Run("table_changes is denied", func(t *testing.T) {
		qerr := c.denied(t, user("u_bob"), "SELECT * FROM TABLE(iceberg.system.table_changes('spike', 'orders_side', NULL, "+snapshot+"))")
		t.Logf("table_changes: %v", qerr)
	})
}
