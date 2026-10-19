// Package catalogs opens the stored credentials of catalog connections.
// Projecting connections into engines is the binders' job
// (internal/bindings).
package catalogs

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrCredentialsUnreadable means a connection's stored credentials cannot be
// opened: missing or rotated key, corrupt or foreign envelope, legacy format,
// or a blob/config that does not parse. Nothing is projected or sent anywhere
// in that case.
var ErrCredentialsUnreadable = errors.New("credentials_unreadable")

// credentialsAADPrefix separates envelope domains: a seal written for one
// table can never be replayed as another row type's credentials.
const credentialsAADPrefix = "catalog_connections:"

// CredentialsAAD is the additional authenticated data binding a credentials
// envelope to its catalog_connections row.
//
// The exact string is a stored-data compatibility contract: every envelope
// ever sealed was authenticated with it, so changing the format here renders
// ALL existing credentials unopenable (ErrCredentialsUnreadable on every row,
// with no recovery but re-entering them by hand). Never rewrite it as a
// cleanup; version the envelope format instead.
func CredentialsAAD(connID pgtype.UUID) string {
	return credentialsAADPrefix + uuid.UUID(connID.Bytes).String()
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

// String reports which fields are set without ever printing their values, so
// a Credentials value can flow through logs and error messages (%v, %+v, %s)
// without leaking client_secret or password.
func (c Credentials) String() string {
	return fmt.Sprintf(
		"Credentials{client_id:%t client_secret:%t realm:%t username:%t password:%t}",
		c.ClientID != "", c.ClientSecret != "", c.Realm != "", c.Username != "", c.Password != "",
	)
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
// a nil error only when none are configured; every other failure — envelope
// OR parse — carries ErrCredentialsUnreadable, so a single errors.Is at the
// call site covers the lot.
func DecodeCredentials(conn db.CatalogConnection, kr *crypto.Keyring) (Credentials, error) {
	plain, err := OpenCredentials(conn, kr)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if plain != nil {
		if err := json.Unmarshal(plain, &c); err != nil {
			return Credentials{}, fmt.Errorf("%w: credentials blob does not parse: %w", ErrCredentialsUnreadable, err)
		}
	}
	if len(conn.Config) > 0 {
		if err := json.Unmarshal(conn.Config, &c); err != nil {
			return Credentials{}, fmt.Errorf("%w: config column does not parse: %w", ErrCredentialsUnreadable, err)
		}
	}
	return c, nil
}
