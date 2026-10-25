package slug

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFrom(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"Revenue by Month", "revenue_by_month"},
		{"  --Já é 2026!  ", "j_2026"},
		{"2026 report", "q_2026_report"},
		{"???", "q"},
		{"", "q"},
		{strings.Repeat("a", 80), strings.Repeat("a", 63)},
	} {
		got := From(c.name)
		if got != c.want {
			t.Errorf("From(%q) = %q, want %q", c.name, got, c.want)
		}
		if !Valid(got) {
			t.Errorf("From(%q) = %q is not a valid slug", c.name, got)
		}
	}
}

func TestValid(t *testing.T) {
	for _, s := range []string{"a", "orders_2026", "a" + strings.Repeat("b", 62)} {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false", s)
		}
	}
	for _, s := range []string{"", "1a", "_a", "A", "a-b", "a" + strings.Repeat("b", 63)} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true", s)
		}
	}
}

func TestUnique(t *testing.T) {
	taken := map[string]bool{"orders": true, "orders_2": true}
	got, err := Unique(context.Background(), "Orders", func(_ context.Context, s string) (bool, error) { return taken[s], nil })
	if err != nil || got != "orders_3" {
		t.Fatalf("Unique = %q, %v; want orders_3", got, err)
	}

	long := strings.Repeat("x", 63)
	got, _ = Unique(context.Background(), long, func(_ context.Context, s string) (bool, error) { return s == long, nil })
	if got != strings.Repeat("x", 61)+"_2" || !Valid(got) {
		t.Errorf("Unique(long) = %q; the suffix must fit in 63 characters", got)
	}

	failed := errors.New("db down")
	if _, err := Unique(context.Background(), "x", func(context.Context, string) (bool, error) { return false, failed }); !errors.Is(err, failed) {
		t.Errorf("Unique = %v, want the lookup error", err)
	}
}
