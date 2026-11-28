package connections

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maltzsama/baleia/internal/db"
)

func TestIsSecretReference(t *testing.T) {
	t.Parallel()
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
	r := SecretResolver{FileBaseDir: dir, EnvAllow: []string{"BALEIA_TEST_REF", "BALEIA_DEFINITELY_MISSING"}}
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
	// The zero value has no base directory, so file references are refused,
	// and it never reads Baleia's own configuration (BALEIA_*).
	if _, err := (SecretResolver{}).Resolve("@baleia-secret[file:token.txt]"); !errors.Is(err, ErrSecretReference) {
		t.Errorf("file without a base directory = %v", err)
	}
	t.Setenv("BALEIA_TEST_REF", "s3cret")
	if v, err := (SecretResolver{}).Resolve("@baleia-secret[env:BALEIA_TEST_REF]"); !errors.Is(err, ErrSecretReference) {
		t.Errorf("env with the zero value = %q, %v, want refused", v, err)
	}
}

// TestSecretResolver_EnvAllow: an env: reference reads only a variable the
// operator allowed, by name or by prefix, so a connection writer cannot read
// the API's own environment (JWT_SECRET, the encryption key).
func TestSecretResolver_EnvAllow(t *testing.T) {
	t.Setenv("JWT_SECRET", "jwt")
	t.Setenv("CONN_PG_PASSWORD", "pg")
	t.Setenv("LAKE_TOKEN", "lake")
	r := SecretResolver{EnvAllow: []string{"CONN_*", "LAKE_TOKEN"}}
	for ref, want := range map[string]string{
		"@baleia-secret[env:CONN_PG_PASSWORD]": "pg",
		"@baleia-secret[env:LAKE_TOKEN]":       "lake",
	} {
		if v, err := r.Resolve(ref); err != nil || v != want {
			t.Errorf("%s = %q, %v", ref, v, err)
		}
		if !r.Permits(ref) {
			t.Errorf("%s should be permitted", ref)
		}
	}
	for _, ref := range []string{"@baleia-secret[env:JWT_SECRET]", "@baleia-secret[env:LAKE_TOKEN_OLD]"} {
		if v, err := r.Resolve(ref); !errors.Is(err, ErrSecretReference) || v != "" {
			t.Errorf("%s = %q, %v, want refused", ref, v, err)
		}
		if r.Permits(ref) {
			t.Errorf("%s should not be permitted", ref)
		}
	}
	if !r.Permits("@baleia-secret[file:token.txt]") || !r.Permits("literal") {
		t.Error("file references and literals are not subject to the env allow-list")
	}

	// A variable Baleia stores itself resolves, but a request may not store it.
	t.Setenv("BALEIA_TRINO_PASSWORD", "trino")
	r.SystemEnv = []string{"BALEIA_TRINO_PASSWORD"}
	system := "@baleia-secret[env:BALEIA_TRINO_PASSWORD]"
	if v, err := r.Resolve(system); err != nil || v != "trino" || !r.Permits(system) {
		t.Errorf("system variable = %q, %v", v, err)
	}
	if r.Writable(system) {
		t.Error("a request must not store a system variable reference")
	}
	if !r.Writable("@baleia-secret[env:CONN_PG_PASSWORD]") || !r.Writable("@baleia-secret[file:token.txt]") || !r.Writable("literal") {
		t.Error("allowed values are writable")
	}
}

// TestSecretResolver_OwnEnv: without BALEIA_SECRET_ENV_ALLOW a connection
// reads any variable but Baleia's own configuration, so references stored
// before the allow-list keep working while JWT_SECRET and the like do not.
func TestSecretResolver_OwnEnv(t *testing.T) {
	t.Setenv("PG_PASSWORD", "pg")
	for _, name := range []string{"JWT_SECRET", "DATABASE_URL", "BALEIA_ENCRYPTION_KEY", "POLARIS_CLIENT_SECRET", "S3_SECRET_KEY", "SMTP_PASSWORD", "AWS_SECRET_ACCESS_KEY", "OTEL_EXPORTER_OTLP_HEADERS"} {
		t.Setenv(name, "own")
	}
	var r SecretResolver
	if v, err := r.Resolve("@baleia-secret[env:PG_PASSWORD]"); err != nil || v != "pg" {
		t.Errorf("PG_PASSWORD = %q, %v", v, err)
	}
	if !r.Writable("@baleia-secret[env:PG_PASSWORD]") {
		t.Error("PG_PASSWORD should be writable")
	}
	for _, name := range []string{"JWT_SECRET", "DATABASE_URL", "BALEIA_ENCRYPTION_KEY", "POLARIS_CLIENT_SECRET", "S3_SECRET_KEY", "SMTP_PASSWORD", "AWS_SECRET_ACCESS_KEY", "OTEL_EXPORTER_OTLP_HEADERS"} {
		ref := "@baleia-secret[env:" + name + "]"
		if v, err := r.Resolve(ref); !errors.Is(err, ErrSecretReference) || v != "" {
			t.Errorf("%s = %q, %v, want refused", name, v, err)
		}
		if r.Permits(ref) || r.Writable(ref) {
			t.Errorf("%s should be refused", name)
		}
	}
	// BALEIA_SECRET_ENV_ALLOW may name one of them on purpose.
	r.EnvAllow = []string{"POLARIS_CLIENT_SECRET"}
	if !r.Writable("@baleia-secret[env:POLARIS_CLIENT_SECRET]") || r.Writable("@baleia-secret[env:PG_PASSWORD]") {
		t.Error("a set allow-list is the only list")
	}
}

func TestParseEnvAllow(t *testing.T) {
	t.Parallel()
	got, err := ParseEnvAllow(" CONN_* , LAKE_TOKEN,,")
	if err != nil || len(got) != 2 || got[0] != "CONN_*" || got[1] != "LAKE_TOKEN" {
		t.Fatalf("ParseEnvAllow = %v, %v", got, err)
	}
	if got, err := ParseEnvAllow(""); err != nil || got != nil {
		t.Fatalf("empty = %v, %v", got, err)
	}
	for _, bad := range []string{"*", "lower", "A*B", "A**", "A-B", ",", " , ", " "} {
		if _, err := ParseEnvAllow(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestConfigReferenceAndSecret(t *testing.T) {
	t.Setenv("BALEIA_TEST_REF", "s3cret")
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	reg = reg.WithSecrets(SecretResolver{EnvAllow: []string{"BALEIA_TEST_REF", "BALEIA_DEFINITELY_MISSING"}})
	ref := reg.NewConfig(db.UUID{}, KindPostgreSQL)
	ref.Secrets["password"] = "@baleia-secret[env:BALEIA_TEST_REF]"
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
	missing := reg.NewConfig(db.UUID{}, KindPostgreSQL)
	missing.Secrets["password"] = "@baleia-secret[env:BALEIA_DEFINITELY_MISSING]"
	if missing.Secret("password") != "" {
		t.Error("unresolvable reference should resolve to empty")
	}
	if missing.Reference("password") == "" {
		t.Error("the raw reference must still be available")
	}
	if len(ref.RefusedReferences()) != 0 || len(missing.RefusedReferences()) != 0 {
		t.Error("allowed references are not refused")
	}

	// A variable outside the allow-list is neither resolved nor projected,
	// and the write path is told which field holds it.
	refused := reg.NewConfig(db.UUID{}, KindPostgreSQL)
	refused.Secrets["password"] = "@baleia-secret[env:JWT_SECRET]"
	refused.Secrets["token"] = "plain"
	if refused.Secret("password") != "" || refused.Reference("password") != "" {
		t.Errorf("refused reference: secret=%q reference=%q", refused.Secret("password"), refused.Reference("password"))
	}
	if got := refused.RefusedReferences(); len(got) != 1 || got[0] != "password" {
		t.Errorf("RefusedReferences = %v", got)
	}
	if got := refused.RefusedReferences("token"); len(got) != 0 {
		t.Errorf("RefusedReferences(token) = %v, only the named keys are checked", got)
	}
}

// TestRegistryConfig_ResolvesWithTheRegistrySecrets: a Config the registry
// builds resolves file references under the registry's base directory, not
// the environment's.
func TestRegistryConfig_ResolvesWithTheRegistrySecrets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cfg := reg.WithSecrets(SecretResolver{FileBaseDir: dir}).NewConfig(db.UUID{}, KindPostgreSQL)
	cfg.Secrets["password"] = "@baleia-secret[file:token.txt]"
	if got := cfg.Secret("password"); got != "from-file" {
		t.Fatalf("Secret = %q, want from-file", got)
	}
}

// TestSecretResolver_FileStaysUnderTheBaseDir: a symlink that escapes the base
// directory is refused, and so is one with an absolute target; a relative one
// that stays inside it (how a Kubernetes Secret volume links name ->
// ..data/name) resolves.
func TestSecretResolver_FileStaysUnderTheBaseDir(t *testing.T) {
	t.Parallel()
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
	// An absolute target is refused even when it stays inside the directory.
	if err := os.Symlink(filepath.Join(dir, "..data", "token"), filepath.Join(dir, "absolute")); err != nil {
		t.Fatal(err)
	}
	r := SecretResolver{FileBaseDir: dir}
	if v, err := r.Resolve("@baleia-secret[file:token]"); err != nil || v != "mounted" {
		t.Errorf("symlink inside the base dir = %q, %v; want mounted", v, err)
	}
	if v, err := r.Resolve("@baleia-secret[file:escape]"); !errors.Is(err, ErrSecretReference) || v != "" {
		t.Errorf("symlink out of the base dir = %q, %v; want ErrSecretReference", v, err)
	}
	if v, err := r.Resolve("@baleia-secret[file:absolute]"); !errors.Is(err, ErrSecretReference) || v != "" {
		t.Errorf("absolute symlink inside the base dir = %q, %v; want ErrSecretReference", v, err)
	}
}
