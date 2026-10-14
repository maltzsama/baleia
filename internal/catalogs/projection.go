package catalogs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrCredentialsUnreadable means a connection's stored credentials cannot be
// opened (missing or rotated key, corrupt or foreign envelope, legacy format).
// Nothing is projected or sent anywhere in that case.
var ErrCredentialsUnreadable = errors.New("credentials_unreadable")

// CredentialsAAD is the additional authenticated data binding a credentials
// envelope to its catalog_connections row.
func CredentialsAAD(connID pgtype.UUID) string {
	return "catalog_connections:" + uuid.UUID(connID.Bytes).String()
}

// RegistryRow mirrors the trino_catalog_registry columns the
// baleia-trino-catalog-store plugin reads at Trino boot. Properties is a flat
// string→string map exactly as the Trino connector expects in a .properties
// file (every value must be a string; numbers and booleans are quoted by the
// caller). The connector.name key is NOT included — it lives in ConnectorName.
//
// Properties ALWAYS contain REAL values. The DDL generator
// (internal/trino.Client.CreateCatalog) is the only place where secrets are
// replaced by the @baleia-secret[...] placeholder, and it does so only in
// the DDL text — never in the registry.
type RegistryRow struct {
	ClusterID     [16]byte
	CatalogName   string
	ConnectorName string
	Properties    map[string]string
}

// Projection bundles the registry row (real values) with the list of
// property keys that the DDL generator must substitute with the
// @baleia-secret[<catalog>:<key>] placeholder.
//
// The split is what lets registry and query log be correct at the same time:
//   - Registry carries the real secret so the plugin can load it at boot.
//   - DDL text carries only the placeholder so system.runtime.queries
//     (the Trino Web UI equivalent) never shows the real secret.
//
// See CR-017 §I1 for the bug this replaces (the old single-map-with-flag
// approach inverted the contract and broke the plugin boot).
type Projection struct {
	Row        RegistryRow
	SecretKeys []string
}

// Credentials is the union of credentials shapes used across catalog
// connection types. Only the fields relevant to the given connection type
// are populated by DecodeCredentials.
type Credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Realm        string `json:"realm"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

// OpenCredentials returns a connection's decrypted credentials JSON, or nil when
// none are stored. It never returns the stored value on failure: an unopenable
// envelope yields ErrCredentialsUnreadable.
func OpenCredentials(conn db.CatalogConnection, kr *crypto.Keyring) ([]byte, error) {
	if conn.CredentialsEnc == nil || *conn.CredentialsEnc == "" {
		return nil, nil
	}
	if kr == nil {
		return nil, fmt.Errorf("%w: BALEIA_ENCRYPTION_KEY is not configured", ErrCredentialsUnreadable)
	}
	plain, err := kr.Open(*conn.CredentialsEnc, CredentialsAAD(conn.ID))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCredentialsUnreadable, err)
	}
	return plain, nil
}

// DecodeCredentials opens and parses a connection's stored credentials. The
// non-secret parts (client_id, realm, username) live in the config column for
// rows written through the provider registry and in the credentials blob for
// older rows; both are read, config winning (an update rewrites config but
// keeps a blob that has no secret to replace). It returns zero Credentials and
// a nil error only when none are configured.
func DecodeCredentials(conn db.CatalogConnection, kr *crypto.Keyring) (Credentials, error) {
	plain, err := OpenCredentials(conn, kr)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if plain != nil {
		if err := json.Unmarshal(plain, &c); err != nil {
			return Credentials{}, fmt.Errorf("decode credentials: %w", err)
		}
	}
	if len(conn.Config) > 0 {
		if err := json.Unmarshal(conn.Config, &c); err != nil {
			return Credentials{}, fmt.Errorf("decode config: %w", err)
		}
	}
	return c, nil
}

// Project builds the registry row (with REAL property values) plus the list
// of secret keys that the DDL generator should replace with the
// @baleia-secret[...] placeholder.
//
// Pure: no DB writes, no DDL. The caller wraps it in a transaction with the
// catalog INSERT and the registry UPSERT, then emits the DDL separately
// using the returned SecretKeys.
func Project(conn db.CatalogConnection, cat db.Catalog, creds Credentials) (Projection, error) {
	if err := validateProjectInputs(conn, cat); err != nil {
		return Projection{}, err
	}

	clusterID, err := uuidToBytes(cat.ClusterID)
	if err != nil {
		return Projection{}, fmt.Errorf("invalid cluster_id: %w", err)
	}

	var (
		props      map[string]string
		secretKeys []string
	)
	switch conn.Type {
	case "polaris", "iceberg", "nessie":
		props, secretKeys = buildIcebergRestProperties(conn, cat, creds)
	case "postgresql", "mysql":
		props, secretKeys = buildSqlProperties(conn, cat, creds)
	case "hive", "glue":
		return Projection{}, fmt.Errorf("connection type %q not yet supported by Project", conn.Type)
	default:
		return Projection{}, fmt.Errorf("unknown connection type %q", conn.Type)
	}

	mergeExtra(props, cat.ExtraProps)

	row := RegistryRow{
		ClusterID:     clusterID,
		CatalogName:   cat.TrinoCatalogName,
		ConnectorName: cat.Connector,
		Properties:    props,
	}
	return Projection{Row: row, SecretKeys: secretKeys}, nil
}

// validateProjectInputs enforces the same CHECK constraints the database has,
// but earlier, so we get nicer error messages for callers and avoid leaking
// partial state into transactions.
func validateProjectInputs(conn db.CatalogConnection, cat db.Catalog) error {
	if !cat.ClusterID.Valid {
		return errors.New("catalog has no cluster_id")
	}
	if cat.TrinoCatalogName == "" {
		return errors.New("catalog has no trino_catalog_name")
	}
	if cat.Connector == "" {
		return errors.New("catalog has no connector")
	}
	if !nameRe.MatchString(cat.TrinoCatalogName) {
		return fmt.Errorf("invalid trino_catalog_name %q (must match %s)", cat.TrinoCatalogName, nameRe)
	}
	if !nameRe.MatchString(cat.Connector) {
		return fmt.Errorf("invalid connector %q (must match %s)", cat.Connector, nameRe)
	}
	if isReserved(cat.TrinoCatalogName) {
		return fmt.Errorf("trino_catalog_name %q is reserved by Trino", cat.TrinoCatalogName)
	}
	if conn.Type == "" {
		return errors.New("catalog_connection has no type")
	}
	return nil
}

// buildIcebergRestProperties builds the properties map for Iceberg REST
// (Polaris/Nessie) catalogs. The returned secretKeys list tells the DDL
// generator which values to replace with the @baleia-secret[...] placeholder
// in the text of CREATE CATALOG — the registry stores the REAL value.
//
// Includes fs.s3.enabled because Trino's S3 filesystem module must be
// activated even when Polaris vends credentials at runtime. (The CR-014
// rev.3 spec talked about fs.native-s3.enabled, but that property was
// renamed to fs.s3.enabled in Trino 482.)
func buildIcebergRestProperties(conn db.CatalogConnection, cat db.Catalog, creds Credentials) (map[string]string, []string) {
	url := strings.TrimRight(conn.Url, "/") + "/api/catalog"
	credential := creds.ClientID + ":" + creds.ClientSecret
	props := map[string]string{
		"iceberg.catalog.type":                            "rest",
		"iceberg.rest-catalog.uri":                        url,
		"iceberg.rest-catalog.warehouse":                  cat.Warehouse,
		"iceberg.rest-catalog.security":                   "OAUTH2",
		"iceberg.rest-catalog.oauth2.credential":          credential,
		"iceberg.rest-catalog.oauth2.scope":               "PRINCIPAL_ROLE:ALL",
		"iceberg.rest-catalog.vended-credentials-enabled": "true",
		"fs.s3.enabled":                                   "true",
	}
	secretKeys := []string{"iceberg.rest-catalog.oauth2.credential"}
	return props, secretKeys
}

// buildSqlProperties builds the properties map for PostgreSQL / MySQL
// connector catalogs. connection-password is the secret key.
func buildSqlProperties(conn db.CatalogConnection, cat db.Catalog, creds Credentials) (map[string]string, []string) {
	props := map[string]string{
		"connection-url": conn.Url,
	}
	var secretKeys []string
	if creds.Username != "" {
		props["connection-user"] = creds.Username
	}
	if creds.Password != "" {
		props["connection-password"] = creds.Password
		secretKeys = append(secretKeys, "connection-password")
	}
	return props, secretKeys
}

// mergeExtra merges the JSONB extra_props on the catalogs row into the
// property map. The JSON must be a flat string→string object; any other
// shape returns an error from the caller's perspective, but here we silently
// skip to avoid failing the whole projection — the database CHECK constraint
// should already have rejected malformed input. extra_props is for advanced
// users to override anything; it wins over defaults.
func mergeExtra(props map[string]string, extra []byte) {
	if len(extra) == 0 || string(extra) == "null" {
		return
	}
	var extraMap map[string]string
	if err := json.Unmarshal(extra, &extraMap); err != nil {
		return
	}
	for k, v := range extraMap {
		props[k] = v
	}
}
