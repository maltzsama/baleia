// Package trino is the Trino provider: a coordinator Baleia federates
// queries on (engine kind "trino") and whose catalogs it can browse through
// information_schema. Every request goes through connections.Dialer.
package trino

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

const (
	requestTimeout = 60 * time.Second
	pageSize       = 1000
)

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindTrino }
func (*Provider) Label() string              { return "Trino" }
func (*Provider) Family() connections.Family { return connections.FamilyEngine }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleFederated}
}
func (*Provider) EngineKind() string { return "trino" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "url", Label: "Coordinator URL", Type: connections.FieldURL, Required: true, Help: "e.g. http://trino:8080"},
		{Key: "service_user", Label: "Service user", Type: connections.FieldText, Default: "baleia", Help: "Identity for metadata reads and service statements"},
		{Key: "admin_user", Label: "Admin user", Type: connections.FieldText, Default: "baleia-admin", Help: "Identity that cancels queries; never runs user SQL"},
		{Key: "username", Label: "Login user", Type: connections.FieldText, Help: "Password authentication (HTTPS only); must be allowed to impersonate the identities Baleia sends"},
		{Key: "password", Label: "Password", Type: connections.FieldSecret},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	out.Values["url"] = strings.TrimRight(out.Values["url"], "/")
	u, _ := url.Parse(out.Values["url"])
	login, password := out.Values["username"] != "", out.Secrets["password"] != ""
	switch {
	case login != password:
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "password", Message: "login user and password go together"}}}
	case password && u.Scheme != "https":
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "url", Message: "password authentication needs an https URL"}}}
	}
	return out, nil
}

// HTTPClient is the dialer's client, authenticated when the connection has a
// password. The Trino engine uses it too.
func HTTPClient(d *connections.Dialer, cfg connections.Config, timeout time.Duration) *http.Client {
	hc := d.HTTPClient(timeout)
	if cfg.Secret("password") != "" {
		hc = trinoclient.WithBasicAuth(hc, cfg.Value("username"), cfg.Secret("password"))
	}
	return hc
}

func (p *Provider) client(cfg connections.Config) *trinoclient.Client {
	user := cfg.Value("service_user")
	if user == "" {
		user = "baleia"
	}
	return trinoclient.NewWithHTTPClient(cfg.Value("url"), user, HTTPClient(p.dialer, cfg, requestTimeout))
}

// Ping runs a statement: /v1/info answers without authentication.
func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	_, err := p.client(cfg).Execute(ctx, "SELECT 1", "", "")
	return err
}

func (p *Provider) OpenMetadata(ctx context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	c := p.client(cfg)
	if _, err := c.Execute(ctx, "SELECT 1", "", ""); err != nil {
		return nil, err
	}
	return &reader{c: c}, nil
}

// reader reads one catalog's information_schema at a time, read-only by
// construction (SELECTs only, literals quoted by the client package).
type reader struct{ c *trinoclient.Client }

func (r *reader) ListCatalogs(ctx context.Context) ([]connections.CatalogRef, error) {
	names, err := r.c.ListCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]connections.CatalogRef, 0, len(names))
	for _, n := range names {
		if n == "system" {
			continue
		}
		out = append(out, connections.CatalogRef{Name: n, Kind: "trino_catalog"})
	}
	return out, nil
}

// catalogRef in a statement stands for the quoted catalog. It is replaced
// once, before anything else: the rest of the statement holds quoted literals
// that may contain any character.
const catalogRef = "{catalog}"

// query runs sql against catalog's information_schema.
func (r *reader) query(ctx context.Context, catalog, sql string) ([][]any, error) {
	q, err := trinoclient.QuoteIdent(catalog)
	if err != nil {
		return nil, fmt.Errorf("%w: catalog", connections.ErrNotFound)
	}
	res, err := r.c.Execute(ctx, strings.Replace(sql, catalogRef, q, 1), "", "")
	if err != nil {
		var qe *trinoclient.QueryError
		if errors.As(err, &qe) && (qe.ErrorName == "CATALOG_NOT_FOUND" || qe.ErrorName == "SCHEMA_NOT_FOUND" || qe.ErrorName == "TABLE_NOT_FOUND") {
			return nil, fmt.Errorf("%w: %s", connections.ErrNotFound, strings.ToLower(qe.ErrorName))
		}
		return nil, err
	}
	return res.Data, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func (r *reader) ListNamespaces(ctx context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	if len(parent) > 0 {
		return []connections.Namespace{}, nil
	}
	rows, err := r.query(ctx, catalog, `SELECT schema_name FROM {catalog}.information_schema.schemata WHERE schema_name <> 'information_schema' ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	out := make([]connections.Namespace, 0, len(rows))
	for _, row := range rows {
		out = append(out, connections.Namespace{str(row[0])})
	}
	return out, nil
}

func (r *reader) ListTables(ctx context.Context, catalog string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	if len(ns) != 1 {
		return nil, "", fmt.Errorf("%w: namespace %v", connections.ErrNotFound, ns)
	}
	rows, err := r.query(ctx, catalog, `SELECT table_name, table_type FROM {catalog}.information_schema.tables
		WHERE table_schema = `+trinoclient.QuoteString(ns[0])+` AND table_name > `+trinoclient.QuoteString(pageToken)+`
		ORDER BY table_name LIMIT `+fmt.Sprint(pageSize+1))
	if err != nil {
		return nil, "", err
	}
	out := make([]connections.TableRef, 0, len(rows))
	for _, row := range rows {
		kind := "TABLE"
		if str(row[1]) == "VIEW" {
			kind = "VIEW"
		}
		out = append(out, connections.TableRef{Catalog: catalog, Namespace: ns, Name: str(row[0]), Type: kind})
	}
	next := ""
	if len(out) > pageSize {
		out = out[:pageSize]
		next = out[pageSize-1].Name
	}
	return out, next, nil
}

func (r *reader) LoadTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if len(t.Namespace) != 1 {
		return nil, fmt.Errorf("%w: namespace %v", connections.ErrNotFound, t.Namespace)
	}
	where := `table_schema = ` + trinoclient.QuoteString(t.Namespace[0]) + ` AND table_name = ` + trinoclient.QuoteString(t.Name)
	tables, err := r.query(ctx, t.Catalog, `SELECT table_type FROM {catalog}.information_schema.tables WHERE `+where)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("%w: table", connections.ErrNotFound)
	}
	rows, err := r.query(ctx, t.Catalog, `SELECT column_name, data_type, is_nullable, ordinal_position
		FROM {catalog}.information_schema.columns WHERE `+where+` ORDER BY ordinal_position`)
	if err != nil {
		return nil, err
	}
	cols := make([]connections.Column, 0, len(rows))
	for _, row := range rows {
		pos, _ := strconv.Atoi(fmt.Sprint(row[3])) // json.Number
		cols = append(cols, connections.Column{
			Name: str(row[0]), NativeType: str(row[1]), Nullable: str(row[2]) == "YES", Position: pos,
		})
	}
	ref := t
	ref.Type = "TABLE"
	if str(tables[0][0]) == "VIEW" {
		ref.Type = "VIEW"
	}
	return &connections.TableMeta{Ref: ref, Columns: cols, Format: "trino", Properties: map[string]string{}}, nil
}

func (r *reader) Close() error { return nil }
