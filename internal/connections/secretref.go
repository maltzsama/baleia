package connections

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ErrSecretReference means an external secret reference cannot be resolved by
// Baleia (missing environment variable, missing file, no base directory).
var ErrSecretReference = errors.New("secret_reference")

// secretReferenceRe matches an external secret placeholder: an env variable or
// a file under BALEIA_SECRET_FILE_BASE_DIR. Only the declared name is allowed:
// no path separators (the file origin is confined to the base directory).
var secretReferenceRe = regexp.MustCompile(`^@baleia-secret\[(env|file):([A-Za-z0-9._-]+)\]$`)

var envNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// IsSecretReference reports whether v is an external secret reference
// (`@baleia-secret[env:VAR]` or `@baleia-secret[file:name]`).
func IsSecretReference(v string) bool {
	m := secretReferenceRe.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	if m[1] == "env" {
		return envNameRe.MatchString(m[2])
	}
	return !strings.Contains(m[2], "..")
}

// SecretResolver resolves external secret references with Baleia's own
// environment: env: from the process environment, file: under FileBaseDir
// (BALEIA_SECRET_FILE_BASE_DIR).
//
// env: never reads Baleia's own configuration (ownEnv: JWT_SECRET,
// DATABASE_URL, BALEIA_*, …): whoever may write a connection could otherwise
// name it and point the connection at a server they control, which would
// receive it as a password. Any other variable resolves, unless EnvAllow
// (BALEIA_SECRET_ENV_ALLOW) is set: then only the variables it lists do, each
// entry a name or a prefix ending in "*". SystemEnv lists what Baleia itself
// stores in connections it owns (the default engine's BALEIA_TRINO_PASSWORD):
// resolved, never accepted from a request.
type SecretResolver struct {
	FileBaseDir string
	EnvAllow    []string
	SystemEnv   []string
}

// ownEnv is the environment Baleia reads as its own configuration
// (internal/config and the clients it builds: S3, SMTP, Temporal, OTel, the
// cloud SDKs' ambient credentials). A connection's env: reference never reads
// it unless BALEIA_SECRET_ENV_ALLOW names it.
var ownEnv = []string{
	"BALEIA_*", "JWT_SECRET", "DATABASE_URL", "PUBLIC_URL", "PORT", "WORKER_HEALTH_PORT", "CI",
	"RATE_LIMIT_*", "POLARIS_*", "TRINO_*", "TEMPORAL_*", "S3_*", "SMTP_*", "OTEL_*",
	"AWS_*", "GOOGLE_*", "AZURE_*", "E2E_*",
}

// ParseEnvAllow splits a comma-separated BALEIA_SECRET_ENV_ALLOW value. It
// refuses an entry that is not a variable name or a prefix of one ending in
// "*"; a lone "*" is refused too, since it would allow the whole environment.
func ParseEnvAllow(raw string) ([]string, error) {
	var out []string
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name := strings.TrimSuffix(entry, "*")
		if !envNameRe.MatchString(name) || strings.Contains(name, "*") {
			return nil, fmt.Errorf("BALEIA_SECRET_ENV_ALLOW: %q is not a variable name or a prefix ending in *", entry)
		}
		out = append(out, entry)
	}
	// A value that names nothing (",", " ") is a mistake, not the unset
	// default: it would leave every variable but Baleia's own readable.
	if raw != "" && len(out) == 0 {
		return nil, errors.New("BALEIA_SECRET_ENV_ALLOW: no variable named; unset it to allow any variable but Baleia's own")
	}
	return out, nil
}

// envMatches reports whether name matches an entry: equal to it, or starting
// with an entry's prefix before its trailing "*".
func envMatches(entries []string, name string) bool {
	for _, entry := range entries {
		if prefix, ok := strings.CutSuffix(entry, "*"); ok {
			if strings.HasPrefix(name, prefix) {
				return true
			}
			continue
		}
		if name == entry {
			return true
		}
	}
	return false
}

// envReference returns the variable an env: reference names, and whether v is
// one.
func envReference(v string) (string, bool) {
	m := secretReferenceRe.FindStringSubmatch(v)
	if m == nil || m[1] != "env" || !envNameRe.MatchString(m[2]) {
		return "", false
	}
	return m[2], true
}

// connectionEnv reports whether a connection may name variable name: in
// EnvAllow when it is set, else anything outside ownEnv.
func (r SecretResolver) connectionEnv(name string) bool {
	if len(r.EnvAllow) > 0 {
		return envMatches(r.EnvAllow, name)
	}
	return !envMatches(ownEnv, name)
}

// Permits reports whether v may be resolved: a literal and a file: reference
// always may (file: is confined to its directory when read), an env:
// reference for a variable a connection may name or one in SystemEnv.
func (r SecretResolver) Permits(v string) bool {
	if !IsSecretReference(v) {
		return true
	}
	name, ok := envReference(v)
	if !ok {
		return true // a file: reference
	}
	return r.connectionEnv(name) || envMatches(r.SystemEnv, name)
}

// Writable reports whether a request may store v: as Permits, except that an
// env: reference must be one a connection may name, never only in SystemEnv.
func (r SecretResolver) Writable(v string) bool {
	if name, ok := envReference(v); ok {
		return r.connectionEnv(name)
	}
	return r.Permits(v)
}

// Resolve returns the value v refers to. A value that is not a reference is
// returned unchanged.
func (r SecretResolver) Resolve(v string) (string, error) {
	m := secretReferenceRe.FindStringSubmatch(v)
	if m == nil {
		return v, nil
	}
	switch m[1] {
	case "env":
		if !envNameRe.MatchString(m[2]) {
			return "", fmt.Errorf("%w: invalid env name", ErrSecretReference)
		}
		if !r.connectionEnv(m[2]) && !envMatches(r.SystemEnv, m[2]) {
			return "", fmt.Errorf("%w: env %s is Baleia's own configuration or outside BALEIA_SECRET_ENV_ALLOW", ErrSecretReference, m[2])
		}
		val, ok := os.LookupEnv(m[2])
		if !ok {
			return "", fmt.Errorf("%w: env %s is not set", ErrSecretReference, m[2])
		}
		return val, nil
	case "file":
		if r.FileBaseDir == "" {
			return "", fmt.Errorf("%w: BALEIA_SECRET_FILE_BASE_DIR is not set", ErrSecretReference)
		}
		if strings.Contains(m[2], "..") {
			return "", fmt.Errorf("%w: invalid file name", ErrSecretReference)
		}
		// os.Root confines the read to the base directory: a symlink inside it
		// may point within it (a mounted Kubernetes Secret does), never out.
		root, err := os.OpenRoot(r.FileBaseDir)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrSecretReference, err)
		}
		defer func() { _ = root.Close() }()
		b, err := root.ReadFile(m[2])
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrSecretReference, err)
		}
		// A mounted file usually ends with a newline; secrets do not include it.
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return "", fmt.Errorf("%w: unknown origin %q", ErrSecretReference, m[1])
}
