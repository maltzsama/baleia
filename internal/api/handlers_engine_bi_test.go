package api

import (
	"strings"
	"testing"
)

func TestBIFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind, tool string
		wantName   string
		wantText   string
	}{
		{"trino", "tableau", "orders.tds", "class='trino'"},
		{"postgresql", "tableau", "orders.tds", "class='postgres'"},
		{"mysql", "powerbi", "orders.pbids", `"protocol": "mysql"`},
		{"sqlserver", "powerbi", "orders.pbids", `"protocol": "sqlserver"`},
	}
	for _, tc := range cases {
		body, name, ok := biFile(tc.kind, tc.tool, "bi.example.com", "8443", "lake", "sales", "orders")
		if !ok || name != tc.wantName {
			t.Fatalf("%s/%s: ok=%v name=%q", tc.kind, tc.tool, ok, name)
		}
		if !strings.Contains(body, tc.wantText) || !strings.Contains(body, "bi.example.com") {
			t.Fatalf("%s/%s body = %s", tc.kind, tc.tool, body)
		}
		lower := strings.ToLower(body)
		for _, secret := range []string{"password", "secret", "user="} {
			if strings.Contains(lower, secret) {
				t.Fatalf("%s/%s carries %q: %s", tc.kind, tc.tool, secret, body)
			}
		}
	}
	if _, _, ok := biFile("sqlite", "tableau", "h", "1", "c", "s", "t"); ok {
		t.Fatal("sqlite has no supported BI connector")
	}
	if _, _, ok := biFile("trino", "excel", "h", "1", "c", "s", "t"); ok {
		t.Fatal("an unknown tool must not produce a file")
	}
}
