// Package slug makes the portable slugs that name spaces, folders, saved
// queries and semantic content (ADR 0013).
package slug

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

var valid = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Valid reports whether s is a slug: a lower-case letter, then up to 62
// lower-case letters, digits or underscores.
func Valid(s string) bool { return valid.MatchString(s) }

// From turns a human name into a slug: lower-case ASCII, runs of other
// characters collapse to '_', never empty, always starting with a letter, at
// most 63 characters.
func From(name string) string {
	var b strings.Builder
	underscore := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			underscore = false
			continue
		}
		if !underscore {
			b.WriteByte('_')
			underscore = true
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		s = "q"
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "q_" + s
	}
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// Unique is From(name), with _2, _3… appended until exists reports it free in
// its scope.
func Unique(ctx context.Context, name string, exists func(context.Context, string) (bool, error)) (string, error) {
	base := From(name)
	candidate := base
	for n := 2; ; n++ {
		taken, err := exists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		suffix := "_" + strconv.Itoa(n)
		keep := len(base)
		if keep > 63-len(suffix) {
			keep = 63 - len(suffix)
		}
		candidate = base[:keep] + suffix
	}
}
