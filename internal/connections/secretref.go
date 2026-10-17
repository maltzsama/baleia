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

// ResolveSecretReference resolves an external reference with Baleia's own
// environment: env: from the process environment, file: under
// BALEIA_SECRET_FILE_BASE_DIR. A value that is not a reference is returned
// unchanged.
func ResolveSecretReference(v string) (string, error) {
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
		base := os.Getenv("BALEIA_SECRET_FILE_BASE_DIR")
		if base == "" {
			return "", fmt.Errorf("%w: BALEIA_SECRET_FILE_BASE_DIR is not set", ErrSecretReference)
		}
		if strings.Contains(m[2], "..") {
			return "", fmt.Errorf("%w: invalid file name", ErrSecretReference)
		}
		b, err := os.ReadFile(filepath.Join(base, m[2]))
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrSecretReference, err)
		}
		// A mounted file usually ends with a newline; secrets do not include it.
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return "", fmt.Errorf("%w: unknown origin %q", ErrSecretReference, m[1])
}
