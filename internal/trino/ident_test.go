package trino

import "testing"

func TestQuoteIdent(t *testing.T) {
	for in, want := range map[string]string{
		"orders":     `"orders"`,
		`a"b`:        `"a""b"`,
		"x; DROP":    `"x; DROP"`,
		"with space": `"with space"`,
		`"`:          `""""`,
		"ünïcode":    `"ünïcode"`,
		"new\nline":  "\"new\nline\"",
		"semi;--/*":  `"semi;--/*"`,
	} {
		got, err := QuoteIdent(in)
		if err != nil || got != want {
			t.Errorf("QuoteIdent(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a\x00b"} {
		if _, err := QuoteIdent(bad); err == nil {
			t.Errorf("QuoteIdent(%q) accepted", bad)
		}
	}
}

func TestQualifiedName(t *testing.T) {
	got, err := QualifiedName("cat", `we"ird`, "t")
	if err != nil || got != `"cat"."we""ird"."t"` {
		t.Fatalf("QualifiedName = %q, %v", got, err)
	}
	if _, err := QualifiedName("cat", "", "t"); err == nil {
		t.Fatal("an empty part must fail the whole name")
	}
}

func TestQuoteString(t *testing.T) {
	for in, want := range map[string]string{"7d": `'7d'`, "it's": `'it''s'`, `'; DROP`: `'''; DROP'`} {
		if got := QuoteString(in); got != want {
			t.Errorf("QuoteString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateRetention(t *testing.T) {
	for _, ok := range []string{"7d", "12h", "30m", "99999d", "1d"} {
		if err := ValidateRetention(ok); err != nil {
			t.Errorf("ValidateRetention(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "0d", "07d", "7", "d", "7w", "100000d", "7d') --", "7d ", "-7d", "7D"} {
		if err := ValidateRetention(bad); err == nil {
			t.Errorf("ValidateRetention(%q) accepted", bad)
		}
	}
}
