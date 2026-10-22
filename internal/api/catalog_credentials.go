package api

import (
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/jackc/pgx/v5/pgtype"
)

// errNoKeyring means BALEIA_ENCRYPTION_KEY is not configured, so credentials
// cannot be stored.
var errNoKeyring = errors.New("BALEIA_ENCRYPTION_KEY is not configured")

// encodeCredentials marshals a credentials value and seals it for storage in
// catalog_connections.credentials_enc. It is the only function that encrypts
// connection credentials. A nil value yields a nil result, so a missing field
// preserves whatever secret is already stored. The returned string is always an
// envelope bound to connID — never plaintext.
func encodeCredentials(kr *crypto.Keyring, creds any, connID pgtype.UUID) (*string, error) {
	if creds == nil {
		return nil, nil
	}
	if kr == nil {
		return nil, errNoKeyring
	}
	b, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}
	enc, err := kr.Seal(b, catalogs.CredentialsAAD(connID))
	if err != nil {
		return nil, err
	}
	return &enc, nil
}

// createCredentials picks the credentials to store from a create request: the
// typed field for the connection type, else the generic `credentials` object.
// For polaris it falls back to the deployment's POLARIS_CLIENT_ID and
// POLARIS_CLIENT_SECRET (polarisDefault) so the first connection can be seeded
// from the environment. It returns an untyped nil when the request carries
// none.
func createCredentials(body *CreateCatalogConnectionRequest, polarisDefault map[string]string) any {
	switch string(body.Type) {
	case "nessie":
		if body.NessieCredentials != nil {
			return body.NessieCredentials
		}
	case "glue":
		if body.GlueCredentials != nil {
			return body.GlueCredentials
		}
	case "hive":
		if body.HiveCredentials != nil {
			return body.HiveCredentials
		}
	case "iceberg":
		if body.IcebergCredentials != nil {
			return body.IcebergCredentials
		}
	case "postgresql":
		if body.PostgresqlCredentials != nil {
			return body.PostgresqlCredentials
		}
	case "mysql":
		if body.MysqlCredentials != nil {
			return body.MysqlCredentials
		}
	}
	if body.Credentials != nil && len(*body.Credentials) > 0 {
		return *body.Credentials
	}
	if string(body.Type) == "polaris" && polarisDefault != nil {
		return polarisDefault
	}
	return nil
}
