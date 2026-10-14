package providers

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/jackc/pgx/v5/pgtype"
)

// FromStored builds a Config from a catalog_connections row as stored today:
// the url column plus the decrypted credentials JSON. Credential keys the
// provider's schema declares as values (client_id, realm, username, …) move to
// Values; keys the schema does not know are dropped.
//
// It is needed until the connection's config is stored per ConfigSchema (#11
// PR 1b).
func FromStored(reg *connections.Registry, kind connections.Kind, id pgtype.UUID, rawURL string, creds []byte) (connections.Config, error) {
	p, err := reg.Get(kind)
	if err != nil {
		return connections.Config{}, err
	}
	cfg := connections.Config{ConnectionID: id, Kind: kind, Values: map[string]string{}, Secrets: map[string]string{}}

	switch kind {
	case connections.KindPostgreSQL, connections.KindMySQL:
		if err := fromJDBCURL(rawURL, cfg.Values); err != nil {
			return connections.Config{}, err
		}
	default:
		cfg.Values["url"] = strings.TrimRight(rawURL, "/")
	}

	if len(creds) > 0 {
		var m map[string]any
		if err := json.Unmarshal(creds, &m); err != nil {
			return connections.Config{}, fmt.Errorf("decode credentials: %w", err)
		}
		fields := map[string]connections.Field{}
		for _, f := range p.ConfigSchema() {
			fields[f.Key] = f
		}
		for k, v := range m {
			s, ok := v.(string)
			f, known := fields[k]
			if !ok || !known || s == "" {
				continue
			}
			if f.Secret() {
				cfg.Secrets[k] = s
			} else {
				cfg.Values[k] = s
			}
		}
	}
	return cfg, nil
}

// fromJDBCURL reads host, port and database from the JDBC URL the UI stores
// for database connections (jdbc:postgresql://host:port/db).
func fromJDBCURL(raw string, values map[string]string) error {
	u, err := url.Parse(strings.TrimPrefix(raw, "jdbc:"))
	if err != nil || u.Host == "" {
		return fmt.Errorf("stored connection url is not a database url: %w", connections.ErrValidation)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		host, port = u.Host, ""
	}
	values["host"] = host
	if port != "" {
		values["port"] = port
	}
	if db := strings.Trim(u.Path, "/"); db != "" {
		values["database"] = db
	}
	return nil
}
