package connections

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
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

func TestSecretResolver_ResolveEnvFileAndLiteral(t *testing.T) {
	t.Setenv("BALEIA_TEST_REF", "s3cret")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := SecretResolver{FileBaseDir: dir}
	if v, err := r.Resolve("@baleia-secret[env:BALEIA_TEST_REF]"); err != nil || v != "s3cret" {
		t.Errorf("env = %q, %v", v, err)
	}
	if _, err := r.Resolve("@baleia-secret[env:BALEIA_DEFINITELY_MISSING]"); !errors.Is(err, ErrSecretReference) {
		t.Errorf("missing env = %v", err)
	}
	if v, err := r.Resolve("@baleia-secret[file:token.txt]"); err != nil || v != "from-file" {
		t.Errorf("file = %q, %v (the trailing newline is dropped)", v, err)
	}
	if _, err := r.Resolve("@baleia-secret[file:missing.txt]"); !errors.Is(err, ErrSecretReference) {
		t.Errorf("missing file = %v", err)
	}
	// A literal is returned unchanged.
	if v, err := r.Resolve("literal"); err != nil || v != "literal" {
		t.Errorf("literal = %q, %v", v, err)
	}
}

func TestSecretResolver_Resolve(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := (SecretResolver{FileBaseDir: dir}).Resolve("@baleia-secret[file:token.txt]"); err != nil || v != "from-file" {
		t.Errorf("file = %q, %v", v, err)
	}
	// The zero value has no base directory: file references are refused, env
	// references still resolve.
	if _, err := (SecretResolver{}).Resolve("@baleia-secret[file:token.txt]"); !errors.Is(err, ErrSecretReference) {
		t.Errorf("file without a base directory = %v", err)
	}
	t.Setenv("BALEIA_TEST_REF", "s3cret")
	if v, err := (SecretResolver{}).Resolve("@baleia-secret[env:BALEIA_TEST_REF]"); err != nil || v != "s3cret" {
		t.Errorf("env with the zero value = %q, %v", v, err)
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

// TestRegistryConfig_ResolvesWithTheRegistrySecrets: a Config the registry
// builds resolves file references under the registry's base directory, not
// the environment's.
func TestRegistryConfig_ResolvesWithTheRegistrySecrets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cfg := reg.WithSecrets(SecretResolver{FileBaseDir: dir}).NewConfig(pgtype.UUID{}, KindPostgreSQL)
	cfg.Secrets["password"] = "@baleia-secret[file:token.txt]"
	if got := cfg.Secret("password"); got != "from-file" {
		t.Fatalf("Secret = %q, want from-file", got)
	}
}

// TestSecretResolver_FileStaysUnderTheBaseDir: a symlink that escapes the base
// directory is refused; one that stays inside it (how a Kubernetes Secret
// volume links name -> ..data/name) resolves.
func TestSecretResolver_FileStaysUnderTheBaseDir(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("leaked"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "..data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "..data", "token"), []byte("mounted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "token"), filepath.Join(dir, "token")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	r := SecretResolver{FileBaseDir: dir}
	if v, err := r.Resolve("@baleia-secret[file:token]"); err != nil || v != "mounted" {
		t.Errorf("symlink inside the base dir = %q, %v; want mounted", v, err)
	}
	if v, err := r.Resolve("@baleia-secret[file:escape]"); !errors.Is(err, ErrSecretReference) || v != "" {
		t.Errorf("symlink out of the base dir = %q, %v; want ErrSecretReference", v, err)
	}
}
