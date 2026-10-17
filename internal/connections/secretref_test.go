package connections

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIsSecretReference(t *testing.T) {
	for _, ok := range []string{
		"@baleia-secret[env:FOO]",
		"@baleia-secret[file:token.txt]",
		"@baleia-secret[file:a-b_c.1]",
	} {
		if !IsSecretReference(ok) {
			t.Errorf("%q should be a reference", ok)
		}
	}
	for _, bad := range []string{
		"literal",
		"@baleia-secret[env:foo]",   // env names are upper-case
		"@baleia-secret[file:../x]", // no traversal
		"@baleia-secret[file:a/b]",  // no path separators
		"@baleia-secret[vault:x]",   // only env/file in v1
		"@baleia-secret[]",
	} {
		if IsSecretReference(bad) {
			t.Errorf("%q should not be a reference", bad)
		}
	}
}

func TestResolveSecretReference(t *testing.T) {
	t.Setenv("BALEIA_TEST_REF", "s3cret")
	if v, err := ResolveSecretReference("@baleia-secret[env:BALEIA_TEST_REF]"); err != nil || v != "s3cret" {
		t.Errorf("env = %q, %v", v, err)
	}
	if _, err := ResolveSecretReference("@baleia-secret[env:BALEIA_DEFINITELY_MISSING]"); !errors.Is(err, ErrSecretReference) {
		t.Errorf("missing env = %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BALEIA_SECRET_FILE_BASE_DIR", dir)
	if v, err := ResolveSecretReference("@baleia-secret[file:token.txt]"); err != nil || v != "from-file" {
		t.Errorf("file = %q, %v (the trailing newline is dropped)", v, err)
	}
	if _, err := ResolveSecretReference("@baleia-secret[file:missing.txt]"); !errors.Is(err, ErrSecretReference) {
		t.Errorf("missing file = %v", err)
	}
	// A literal is returned unchanged.
	if v, err := ResolveSecretReference("literal"); err != nil || v != "literal" {
		t.Errorf("literal = %q, %v", v, err)
	}
}

func TestConfigReferenceAndSecret(t *testing.T) {
	t.Setenv("BALEIA_TEST_REF", "s3cret")
	ref := Config{Secrets: map[string]string{"password": "@baleia-secret[env:BALEIA_TEST_REF]"}}
	if got := ref.Reference("password"); got != "@baleia-secret[env:BALEIA_TEST_REF]" {
		t.Errorf("Reference = %q", got)
	}
	if got := ref.Secret("password"); got != "s3cret" {
		t.Errorf("Secret resolved = %q", got)
	}

	lit := Config{Secrets: map[string]string{"password": "plain"}}
	if lit.Reference("password") != "" || lit.Secret("password") != "plain" {
		t.Errorf("literal: reference=%q secret=%q", lit.Reference("password"), lit.Secret("password"))
	}

	// An unresolvable reference yields an empty value for Baleia's own use,
	// while the raw reference is still available for the projection.
	missing := Config{Secrets: map[string]string{"password": "@baleia-secret[env:BALEIA_DEFINITELY_MISSING]"}}
	if missing.Secret("password") != "" {
		t.Error("unresolvable reference should resolve to empty")
	}
	if missing.Reference("password") == "" {
		t.Error("the raw reference must still be available")
	}
}
