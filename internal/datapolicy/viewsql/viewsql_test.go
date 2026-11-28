package viewsql

import (
	"slices"
	"testing"

	"github.com/maltzsama/baleia/internal/connections"
	"github.com/maltzsama/baleia/internal/datapolicy"
)

func view(namespace, name, owner, sql string) datapolicy.CatalogView {
	return Resolve([]string{"lake"}, connections.View{
		Namespace: connections.Namespace{namespace}, Name: name, Owner: owner, SQL: sql,
		DefaultCatalog: "lake", DefaultSchema: connections.Namespace{namespace},
	})
}

func TestDefinerReach(t *testing.T) {
	t.Parallel()
	orders := datapolicy.NewTableName("lake", "sales", "orders")
	policied := map[datapolicy.TableName]bool{orders: true, datapolicy.NewTableName("lake", "hr", "people"): true}
	views := []datapolicy.CatalogView{
		view("sales", "v_admin", "baleia_admin", "SELECT * FROM orders"),
		view("sales", "v_user", "u_ana", "SELECT o.id FROM sales.orders o JOIN lake.sales.items i ON o.id = i.order_id"),
		view("sales", "v_invoker", "", "SELECT * FROM orders"),
		// A DEFINER view over an INVOKER view runs the inner one as its owner.
		view("sales", "v_nested", "baleia", "SELECT * FROM v_invoker"),
		view("sales", "v_clean", "baleia_admin", "SELECT * FROM items"),
		view("sales", "v_broken", "baleia", "SELECT * FROM ((("),
		// Views that read each other do not loop.
		view("sales", "v_loop_a", "u_bob", "SELECT * FROM v_loop_b"),
		view("sales", "v_loop_b", "u_bob", "SELECT * FROM v_loop_a UNION ALL SELECT * FROM orders"),
		// In a cycle, every view reaches what any view of the cycle reads.
		view("hr", "v_cycle_a", "u_bob", "SELECT * FROM v_cycle_b UNION ALL SELECT * FROM people"),
		view("hr", "v_cycle_b", "u_bob", "SELECT * FROM v_cycle_a"),
	}
	got := map[string]datapolicy.ViewReach{}
	for _, r := range datapolicy.DefinerReach(views, policied) {
		got[r.View.Table] = r
	}
	for _, name := range []string{"v_admin", "v_user", "v_nested", "v_loop_a", "v_loop_b"} {
		if r, ok := got[name]; !ok || !slices.Contains(r.Reads, orders) || r.Unreadable {
			t.Errorf("%s = %+v (%v), want it to reach lake.sales.orders", name, r, ok)
		}
	}
	people := datapolicy.NewTableName("lake", "hr", "people")
	for _, name := range []string{"v_cycle_a", "v_cycle_b"} {
		if r, ok := got[name]; !ok || !slices.Contains(r.Reads, people) {
			t.Errorf("%s = %+v (%v), want it to reach lake.hr.people", name, r, ok)
		}
	}
	if _, ok := got["v_invoker"]; ok {
		t.Error("an INVOKER view is reported as DEFINER")
	}
	if _, ok := got["v_clean"]; ok {
		t.Error("a DEFINER view over unpolicied tables is reported")
	}
	if r := got["v_broken"]; !r.Unreadable || len(r.Reads) != len(policied) {
		t.Errorf("an unparsable DEFINER view = %+v, want it to reach every policied table", r)
	}

	trusted := datapolicy.TrustedDefinerViews(datapolicy.DefinerReach(views, policied), map[string]bool{"baleia_admin": true, "baleia": true})
	names := make([]string, 0, len(trusted))
	for _, r := range trusted {
		names = append(names, r.View.Table)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"v_admin", "v_broken", "v_nested"}) {
		t.Fatalf("trusted DEFINER views = %v", names)
	}
}
