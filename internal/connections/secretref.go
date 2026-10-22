package connections

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// (BALEIA_SECRET_FILE_BASE_DIR). The zero value resolves env: only.
type SecretResolver struct {
	FileBaseDir string
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
		b, err := os.ReadFile(filepath.Join(r.FileBaseDir, m[2]))
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrSecretReference, err)
		}
		// A mounted file usually ends with a newline; secrets do not include it.
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return "", fmt.Errorf("%w: unknown origin %q", ErrSecretReference, m[1])
}

// ResolveSecretReference resolves v with the base directory read from
// BALEIA_SECRET_FILE_BASE_DIR. It goes away once every Config carries the
// resolver the composition root builds (#367).
func ResolveSecretReference(v string) (string, error) {
	return SecretResolver{FileBaseDir: os.Getenv("BALEIA_SECRET_FILE_BASE_DIR")}.Resolve(v)
}
