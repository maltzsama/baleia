package api

import (
	"context"
	"strconv"
	"strings"
)

// slugify turns a human name into the portable slug used by spaces, folders and
// saved queries (ADR 0013): lower-case ASCII, runs of other characters collapse
// to '_', never empty, always starting with a letter, at most 63 characters.
func slugify(name string) string {
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

// uniqueSlug appends _2, _3… until exists reports the slug free in its scope.
func uniqueSlug(ctx context.Context, name string, exists func(context.Context, string) (bool, error)) (string, error) {
	base := slugify(name)
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
