package catalogs

import (
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5/pgtype"
)

// nameRe matches Trino's catalog/connector name grammar: lowercase ASCII,
// starts with a letter, may contain lowercase ASCII letters, digits and
// underscore, up to 63 characters. Mirrors the SQL CHECK constraints on
// trino_catalog_registry and catalogs trino_catalog_name.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// reservedNames lists Trino built-in catalog names that cannot be reused, plus
// the "env" and "file" origins reserved by the baleia-trino-catalog-store
// SecretResolver's @baleia-secret[<origin>:<key>] syntax (v0.3.0+).
var reservedNames = map[string]struct{}{
	"system": {},
	"jmx":    {},
	"tpch":   {},
	"tpcds":  {},
	"memory": {},
	"env":    {},
	"file":   {},
}

// isReserved reports whether name collides with a Trino built-in.
func isReserved(name string) bool {
	_, ok := reservedNames[name]
	return ok
}

// uuidToBytes unwraps a pgtype.UUID into its 16-byte representation. Returns
// an error if the UUID is NULL or invalid (which should never happen because
// the column is NOT NULL, but it makes the API explicit).
func uuidToBytes(u pgtype.UUID) ([16]byte, error) {
	if !u.Valid {
		return [16]byte{}, errNullUUID
	}
	return u.Bytes, nil
}

var errNullUUID = errors.New("uuid is NULL")