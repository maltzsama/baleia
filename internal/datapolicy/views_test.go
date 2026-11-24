package datapolicy

import "testing"

func TestContent_PoliciedTables(t *testing.T) {
	t.Parallel()
	c := Content{Catalogs: map[string]CatalogContent{"a": {
		Names:  []string{"lake", "Lake2"},
		Tables: map[string]map[string]*TablePolicies{"Sales": {"Orders": {}}},
	}}}
	got := c.PoliciedTables()
	if !got[NewTableName("lake", "sales", "orders")] || !got[NewTableName("lake2", "sales", "orders")] || len(got) != 2 {
		t.Fatalf("PoliciedTables = %v", got)
	}
}
