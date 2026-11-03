package api

import (
	"net/mail"
	"regexp"
	"strings"
)

// engineCatalogNameRe is the identifier pattern the contract declares for
// trino_catalog_name, connector and engine_catalog_name (#617). Validate it
// before the name reaches the engine's DDL.
var engineCatalogNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// validEngineCatalogName reports whether s matches the declared identifier
// pattern.
func validEngineCatalogName(s string) bool { return engineCatalogNameRe.MatchString(s) }

// validEmail reports whether s is a bare e-mail address (format: email).
func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s && strings.Contains(s, "@")
}

// requiredName reports whether a required, trimmed name was supplied.
func requiredName(s string) bool { return strings.TrimSpace(s) != "" }
