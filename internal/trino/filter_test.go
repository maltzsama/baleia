package trino

import "testing"

func TestValidatePartitionFilter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		filter string
		ok     bool
	}{
		{"date comparison", "day < DATE '2026-05-01'", true},
		{"a column named date", "date >= DATE '2026-05-01'", true},
		{"and, or, parentheses", `(region = 'eu' OR region = 'us') AND "event day" <> DATE '2026-01-01'`, true},
		{"in list", "region IN ('eu', 'us', 'it''s')", true},
		{"not in and between", "hour NOT IN (1, 2) AND day BETWEEN DATE '2026-01-01' AND DATE '2026-02-01'", true},
		{"is not null", "region IS NOT NULL", true},
		{"timestamp literal and negatives", "ts > TIMESTAMP '2026-01-01 00:00:00' AND delta > -3.5", true},
		{"empty", "  ", false},
		{"subquery", "region IN (SELECT region FROM secrets)", false},
		{"function call", "lower(region) = 'eu'", false},
		{"statement separator", "day < 1; DROP TABLE t", false},
		{"comment", "day < 1 -- trailing", false},
		{"column compared with a column", "a = b", false},
		{"unterminated string", "region = 'eu", false},
		{"dangling operator", "day <", false},
		{"bare literal", "1 = 1", false},
		{"is without null", "region IS 'eu'", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePartitionFilter(tt.filter)
			if (err == nil) != tt.ok {
				t.Errorf("ValidatePartitionFilter(%q) = %v, want ok=%v", tt.filter, err, tt.ok)
			}
		})
	}
}
