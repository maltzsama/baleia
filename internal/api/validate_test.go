package api

import (
	"strings"
	"testing"
)

func TestValidEmail(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"a@b.co", "first.last+tag@example.com"} {
		if !validEmail(s) {
			t.Errorf("%q: want a valid e-mail", s)
		}
	}
	for _, s := range []string{"", "not-an-email", "a@", "@b", "Name <a@b.co>", " a@b.co "} {
		if validEmail(s) {
			t.Errorf("%q: want an invalid e-mail", s)
		}
	}
}

func TestValidEngineCatalogName(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"tpch", "a", "a_b9", "abc_123"} {
		if !validEngineCatalogName(s) {
			t.Errorf("%q: want a valid identifier", s)
		}
	}
	long := "a" + strings.Repeat("b", 63)
	for _, s := range []string{"", "1abc", "ABC", "a-b", "a b", "_a", long} {
		if validEngineCatalogName(s) {
			t.Errorf("%q: want an invalid identifier", s)
		}
	}
}

func TestRequiredName(t *testing.T) {
	t.Parallel()
	if requiredName("  ") {
		t.Error("a blank name was accepted")
	}
	if !requiredName("ok") {
		t.Error("a non-blank name was rejected")
	}
}
