package api

import "testing"

func TestRedactURLSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain url unchanged", "https://polaris:8181/api/catalog", "https://polaris:8181/api/catalog"},
		{"jdbc url unchanged", "jdbc:postgresql://postgres:5432/baleia", "jdbc:postgresql://postgres:5432/baleia"},
		{"http userinfo", "https://user:pass@host/path", "https://redacted@host/path"},
		{"jdbc userinfo", "jdbc:postgresql://user:pass@host:5432/db", "jdbc:postgresql://redacted@host:5432/db"},
		{"token query", "https://host/path?token=abc123", "https://host/path?token=redacted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactURLSecrets(tc.in); got != tc.want {
				t.Fatalf("redactURLSecrets(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
