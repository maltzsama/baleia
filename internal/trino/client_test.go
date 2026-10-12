package trino

import (
	"strings"
	"testing"
)

func TestBuildCreateCatalogDDL_RedactsSecretKeys(t *testing.T) {
	spec := CatalogSpec{
		Name:      "baleia_polaris",
		Connector: "iceberg",
		Properties: map[string]string{
			"iceberg.rest-catalog.uri":               "http://polaris:8181/api/catalog",
			"iceberg.rest-catalog.oauth2.credential": "abc:def", // secret
			"fs.s3.enabled":                          "true",
		},
		SecretKeys: []string{"iceberg.rest-catalog.oauth2.credential"},
	}
	ddl := buildCreateCatalogDDL(spec)

	// The placeholder must appear in the DDL text where the secret was.
	if !strings.Contains(ddl, "@baleia-secret[baleia_polaris:iceberg.rest-catalog.oauth2.credential]") {
		t.Errorf("DDL missing placeholder:\n%s", ddl)
	}

	// The real secret must NEVER appear in the DDL text.
	if strings.Contains(ddl, "abc:def") {
		t.Errorf("DDL contains the real secret!\n%s", ddl)
	}

	// The non-secret values must appear.
	if !strings.Contains(ddl, "http://polaris:8181/api/catalog") {
		t.Errorf("DDL missing non-secret value:\n%s", ddl)
	}
	if !strings.Contains(ddl, "fs.s3.enabled") {
		t.Errorf("DDL missing fs.s3.enabled:\n%s", ddl)
	}
}

func TestBuildCreateCatalogDDL_SortedKeys(t *testing.T) {
	// Keys out of alphabetical order on purpose: at runtime Go's range order
	// is randomized, so the only way to detect a regression to range-order
	// is to make this test fail when the output is not alphabetically sorted.
	spec := CatalogSpec{
		Name:      "x",
		Connector: "tpch",
		Properties: map[string]string{
			"zzz": "3",
			"aaa": "1",
			"mmm": "2",
		},
	}
	ddl := buildCreateCatalogDDL(spec)

	// Extract the order of property keys as they appear in the DDL.
	// They should be aaa, mmm, zzz.
	wantOrder := []string{"\"aaa\"", "\"mmm\"", "\"zzz\""}
	pos := 0
	for _, want := range wantOrder {
		idx := strings.Index(ddl[pos:], want)
		if idx < 0 {
			t.Errorf("key %s not found after position %d in DDL:\n%s", want, pos, ddl)
			return
		}
		pos += idx + len(want)
	}

	// Sanity check: the full DDL should be deterministic regardless of map
	// iteration order. Re-run and confirm it matches.
	ddl2 := buildCreateCatalogDDL(spec)
	if ddl != ddl2 {
		t.Errorf("DDL is non-deterministic across calls:\nDDL1:\n%s\nDDL2:\n%s", ddl, ddl2)
	}
}

func TestBuildCreateCatalogDDL_EscapesSingleQuote(t *testing.T) {
	// A password like "abc'def" must be escaped to "abc''def" in the DDL.
	// Without escaping, the value would terminate the SQL string literal
	// and either break the DDL or open an injection path.
	spec := CatalogSpec{
		Name:      "pg_test",
		Connector: "postgresql",
		Properties: map[string]string{
			"connection-url":      "jdbc:postgresql://host/db",
			"connection-user":     "alice",
			"connection-password": "abc'def",
		},
		SecretKeys: []string{"connection-password"},
	}
	ddl := buildCreateCatalogDDL(spec)

	if !strings.Contains(ddl, "'abc''def'") {
		// wait — this password IS a secret key, so it'll be placeholderized,
		// not escaped. Test the escape path with a non-secret value instead.
	}

	// Non-secret: connection-url contains no quote, but let's also test
	// an explicit non-secret with a single quote to exercise the escape path.
	spec2 := CatalogSpec{
		Name:      "pg_test",
		Connector: "postgresql",
		Properties: map[string]string{
			"connection-url": "jdbc:postgresql://host'db",
		},
	}
	ddl2 := buildCreateCatalogDDL(spec2)
	if !strings.Contains(ddl2, "'jdbc:postgresql://host''db'") {
		t.Errorf("single quote not escaped in DDL:\n%s", ddl2)
	}

	_ = ddl // reference to avoid unused warning
}

func TestValidateCatalogName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"", false},
		{"a", true},
		{"baleia_polaris", true},
		{"Baleia_Polaris", false}, // uppercase
		{"baleia-polaris", false},  // hyphen
		{"1baleia", false},         // starts with digit
		{"system", true},           // regex allows it; reserved check is elsewhere
		{"_baleia", false},         // starts with underscore
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false}, // too long
	}
	for _, c := range cases {
		err := validateCatalogName(c.name)
		if (err == nil) != c.ok {
			t.Errorf("validateCatalogName(%q) err=%v; want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestCreateCatalog_RejectsInvalidNameBeforeExecute(t *testing.T) {
	c := &Client{baseURL: "http://invalid:1", user: "test", http: nil}
	err := c.CreateCatalog(nil, CatalogSpec{
		Name:      "BadName",
		Connector: "iceberg",
		Properties: map[string]string{"a": "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid catalog name") {
		t.Errorf("expected validation error, got %v", err)
	}
}