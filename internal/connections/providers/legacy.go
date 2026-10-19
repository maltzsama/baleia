package providers

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/jackc/pgx/v5/pgtype"
)

// FromStored builds a Config from a catalog_connections row: the config column
// (non-secret values), the decrypted credentials JSON and, for rows written
// before the config column existed, the url column. Credential keys the
// provider's schema declares as values (client_id, realm, username, …) move to
// Values when config does not set them; keys the schema does not know are
// dropped. A stored config or credentials blob that does not parse is stored
// data that cannot be read, so it carries ErrCredentialsUnreadable like an
// unopenable envelope does (#349): one errors.Is at the call site covers the
// lot, and the caller reports 422 rather than a server failure.
func FromStored(reg *connections.Registry, kind connections.Kind, id pgtype.UUID, rawURL string, config, creds []byte) (connections.Config, error) {
	values := map[string]string{}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &values); err != nil {
			return connections.Config{}, fmt.Errorf("%w: decode config: %w", catalogs.ErrCredentialsUnreadable, err)
		}
	}
	var secrets map[string]any
	if len(creds) > 0 {
		if err := json.Unmarshal(creds, &secrets); err != nil {
			return connections.Config{}, fmt.Errorf("%w: decode credentials: %w", catalogs.ErrCredentialsUnreadable, err)
		}
	}
	return build(reg, kind, id, rawURL, values, secrets, false)
}

// FromRequest builds a Config from an API request in either shape: config and
// credentials split per the provider's schema, or the older url plus a
// credentials object that also carries non-secret fields. Unknown keys are
// kept, so Validate reports them.
func FromRequest(reg *connections.Registry, kind connections.Kind, id pgtype.UUID, rawURL string, config map[string]string, creds map[string]any) (connections.Config, error) {
	values := map[string]string{}
	for k, v := range config {
		if v != "" {
			values[k] = v
		}
	}
	return build(reg, kind, id, rawURL, values, creds, true)
}

// Merge applies a partial update to a stored config. Config keys set values
// (an empty string clears one); credential keys replace the secrets they name
// (an empty value keeps the stored one); a url replaces the endpoint. Unknown
// keys are kept, so Validate reports them.
func Merge(reg *connections.Registry, cur connections.Config, rawURL *string, config map[string]string, creds map[string]any) (connections.Config, error) {
	p, err := reg.Get(cur.Kind)
	if err != nil {
		return connections.Config{}, err
	}
	out := connections.Config{ConnectionID: cur.ConnectionID, Kind: cur.Kind, Values: map[string]string{}, Secrets: map[string]string{}}
	for k, v := range cur.Values {
		out.Values[k] = v
	}
	for k, v := range cur.Secrets {
		out.Secrets[k] = v
	}
	if rawURL != nil {
		if err := applyURL(cur.Kind, *rawURL, out.Values, true); err != nil {
			return connections.Config{}, err
		}
	}
	for k, v := range config {
		if v == "" {
			delete(out.Values, k)
		} else {
			out.Values[k] = v
		}
	}
	fields := schemaFields(p)
	for k, raw := range creds {
		v, ok := raw.(string)
		if !ok || v == "" {
			continue
		}
		if f, known := fields[k]; known && !f.Secret() {
			out.Values[k] = v
		} else {
			out.Secrets[k] = v
		}
	}
	return out, nil
}

func build(reg *connections.Registry, kind connections.Kind, id pgtype.UUID, rawURL string, values map[string]string, creds map[string]any, keepUnknown bool) (connections.Config, error) {
	p, err := reg.Get(kind)
	if err != nil {
		return connections.Config{}, err
	}
	cfg := connections.Config{ConnectionID: id, Kind: kind, Values: values, Secrets: map[string]string{}}
	// The backfilled config of a database row holds its JDBC URL under "url";
	// it wins over the url column, which only fills what is still missing.
	if isDatabase(kind) {
		if u, ok := cfg.Values["url"]; ok {
			delete(cfg.Values, "url")
			if err := applyURL(kind, u, cfg.Values, false); err != nil {
				return connections.Config{}, err
			}
		}
	}
	if err := applyURL(kind, rawURL, cfg.Values, false); err != nil {
		return connections.Config{}, err
	}

	fields := schemaFields(p)
	for k, raw := range creds {
		v, ok := raw.(string)
		if ok && v == "" {
			continue // forms send every input, filled or not
		}
		f, known := fields[k]
		switch {
		case !known && keepUnknown:
			cfg.Secrets[k] = fmt.Sprint(raw)
		case !known, !ok, v == "":
		case f.Secret():
			cfg.Secrets[k] = v
		case cfg.Values[k] == "":
			cfg.Values[k] = v
		}
	}
	return cfg, nil
}

func schemaFields(p connections.Provider) map[string]connections.Field {
	fields := map[string]connections.Field{}
	for _, f := range p.ConfigSchema() {
		fields[f.Key] = f
	}
	return fields
}

func isDatabase(kind connections.Kind) bool {
	return kind == connections.KindPostgreSQL || kind == connections.KindMySQL
}

// applyURL reads the older url field: the endpoint of REST kinds, a JDBC URL
// for databases. With override false it fills only what is not set yet.
func applyURL(kind connections.Kind, raw string, values map[string]string, override bool) error {
	if raw == "" {
		return nil
	}
	if !isDatabase(kind) {
		if override || values["url"] == "" {
			values["url"] = strings.TrimRight(raw, "/")
		}
		return nil
	}
	if !override && values["host"] != "" {
		return nil
	}
	return fromJDBCURL(raw, values)
}

// fromJDBCURL reads host, port and database from the JDBC URL the UI stores
// for database connections (jdbc:postgresql://host:port/db).
func fromJDBCURL(raw string, values map[string]string) error {
	u, err := url.Parse(strings.TrimPrefix(raw, "jdbc:"))
	if err != nil || u.Host == "" {
		return &connections.ValidationError{Fields: []connections.FieldError{{Key: "url", Message: "must be a database URL like jdbc:postgresql://host:5432/db"}}}
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

// LegacyURL is the url column value for a validated config. The Trino
// projection still reads that column (#80): the endpoint for REST kinds, a JDBC
// URL for databases.
func LegacyURL(cfg connections.Config) string {
	if !isDatabase(cfg.Kind) {
		return cfg.Value("url")
	}
	u := fmt.Sprintf("jdbc:%s://%s", cfg.Kind, net.JoinHostPort(cfg.Value("host"), cfg.Value("port")))
	if db := cfg.Value("database"); db != "" {
		u += "/" + db
	}
	return u
}
