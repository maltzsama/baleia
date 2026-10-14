// Package clickhouse is the ClickHouse provider. It talks to the HTTP
// interface (no driver): every request goes through connections.Dialer, runs
// with readonly=1 and a time limit, and passes values as query parameters
// ({name:String}), never in the SQL text.
package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
)

const (
	statementTimeout = 30 * time.Second
	pageSize         = 1000
)

// Catalog is the one catalog of a ClickHouse server; its namespaces are the
// databases.
const Catalog = "clickhouse"

var systemDatabases = []string{"system", "INFORMATION_SCHEMA", "information_schema"}

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindClickHouse }
func (*Provider) Label() string              { return "ClickHouse" }
func (*Provider) Family() connections.Family { return connections.FamilyDatabase }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleDirect}
}
func (*Provider) EngineKind() string { return "clickhouse" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "host", Label: "Host", Type: connections.FieldText, Required: true},
		{Key: "port", Label: "HTTP port", Type: connections.FieldInt, Default: "8123", Help: "8123 for HTTP, 8443 for HTTPS"},
		{Key: "secure", Label: "HTTPS", Type: connections.FieldBool, Default: "false"},
		{Key: "database", Label: "Default database", Type: connections.FieldText, Default: "default"},
		{Key: "username", Label: "Username", Type: connections.FieldText, Default: "default"},
		{Key: "password", Label: "Password", Type: connections.FieldSecret},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	if err := connections.Port(out.Values["port"]); err != nil {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "port", Message: err.Error()}}}
	}
	if strings.ContainsAny(out.Values["host"], "/ ,@?#") {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "host", Message: "must be a single host name or address"}}}
	}
	return out, nil
}

// client is one ClickHouse HTTP endpoint with credentials.
type client struct {
	base     string
	user     string
	password string
	database string
	http     *http.Client
}

func (p *Provider) client(cfg connections.Config) *client {
	scheme := "http"
	if cfg.Value("secure") == "true" {
		scheme = "https"
	}
	port := cfg.Value("port")
	if port == "" {
		port = "8123"
	}
	return &client{
		base:     scheme + "://" + net.JoinHostPort(cfg.Value("host"), port) + "/",
		user:     valueOr(cfg.Value("username"), "default"),
		password: cfg.Secret("password"),
		database: valueOr(cfg.Value("database"), "default"),
		http:     p.dialer.HTTPClient(statementTimeout + 5*time.Second),
	}
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// HTTPError is a ClickHouse error answer; Code is the exception code from the
// X-ClickHouse-Exception-Code header.
type HTTPError struct {
	Status int
	Code   int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("clickhouse request failed (HTTP %d, code %d)", e.Status, e.Code)
}

// query runs a read-only statement and decodes FORMAT JSONCompact rows.
func (c *client) query(ctx context.Context, sql string, params map[string]string) ([][]json.RawMessage, error) {
	q := url.Values{}
	q.Set("database", c.database)
	q.Set("readonly", "1")
	q.Set("max_execution_time", strconv.Itoa(int(statementTimeout.Seconds())))
	q.Set("default_format", "JSONCompact")
	for k, v := range params {
		q.Set("param_"+k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"?"+q.Encode(), strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-ClickHouse-User", c.user)
	req.Header.Set("X-ClickHouse-Key", c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		code, _ := strconv.Atoi(resp.Header.Get("X-ClickHouse-Exception-Code"))
		if code == 81 || code == 60 { // UNKNOWN_DATABASE, UNKNOWN_TABLE
			return nil, fmt.Errorf("%w: clickhouse code %d", connections.ErrNotFound, code)
		}
		return nil, &HTTPError{Status: resp.StatusCode, Code: code}
	}
	var out struct {
		Data [][]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("clickhouse decode: %w", err)
	}
	return out.Data, nil
}

func str(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return strings.Trim(string(v), `"`)
}

func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	_, err := p.client(cfg).query(ctx, "SELECT 1", nil)
	return err
}

func (p *Provider) OpenMetadata(ctx context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	c := p.client(cfg)
	if _, err := c.query(ctx, "SELECT 1", nil); err != nil {
		return nil, err
	}
	return &reader{c: c}, nil
}

type reader struct{ c *client }

func (r *reader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	return []connections.CatalogRef{{Name: Catalog, Kind: "server"}}, nil
}

func checkCatalog(c string) error {
	if c != Catalog {
		return fmt.Errorf("%w: catalog", connections.ErrNotFound)
	}
	return nil
}

func (r *reader) ListNamespaces(ctx context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	if err := checkCatalog(catalog); err != nil {
		return nil, err
	}
	if len(parent) > 0 {
		return []connections.Namespace{}, nil
	}
	rows, err := r.c.query(ctx, `SELECT name FROM system.databases WHERE name NOT IN ({s1:String}, {s2:String}, {s3:String}) ORDER BY name`,
		map[string]string{"s1": systemDatabases[0], "s2": systemDatabases[1], "s3": systemDatabases[2]})
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
	if err := checkCatalog(catalog); err != nil {
		return nil, "", err
	}
	if len(ns) != 1 {
		return nil, "", fmt.Errorf("%w: namespace %v", connections.ErrNotFound, ns)
	}
	rows, err := r.c.query(ctx, `
		SELECT name, engine FROM system.tables
		WHERE database = {db:String} AND name > {after:String} AND NOT is_temporary
		ORDER BY name LIMIT {limit:UInt32}`,
		map[string]string{"db": ns[0], "after": pageToken, "limit": strconv.Itoa(pageSize + 1)})
	if err != nil {
		return nil, "", err
	}
	out := make([]connections.TableRef, 0, len(rows))
	for _, row := range rows {
		kind := "TABLE"
		switch str(row[1]) {
		case "View":
			kind = "VIEW"
		case "MaterializedView":
			kind = "MATERIALIZED_VIEW"
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
	if err := checkCatalog(t.Catalog); err != nil {
		return nil, err
	}
	if len(t.Namespace) != 1 {
		return nil, fmt.Errorf("%w: namespace %v", connections.ErrNotFound, t.Namespace)
	}
	params := map[string]string{"db": t.Namespace[0], "table": t.Name}
	tables, err := r.c.query(ctx, `SELECT engine, comment FROM system.tables WHERE database = {db:String} AND name = {table:String}`, params)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("%w: table", connections.ErrNotFound)
	}
	rows, err := r.c.query(ctx, `
		SELECT name, type, comment, position FROM system.columns
		WHERE database = {db:String} AND table = {table:String}
		ORDER BY position`, params)
	if err != nil {
		return nil, err
	}
	cols := make([]connections.Column, 0, len(rows))
	for _, row := range rows {
		typ := str(row[1])
		pos, _ := strconv.Atoi(str(row[3]))
		cols = append(cols, connections.Column{
			Name: str(row[0]), NativeType: typ, Comment: str(row[2]), Position: pos,
			Nullable: strings.HasPrefix(typ, "Nullable("),
		})
	}
	engine := str(tables[0][0])
	ref := t
	ref.Type = "TABLE"
	switch engine {
	case "View":
		ref.Type = "VIEW"
	case "MaterializedView":
		ref.Type = "MATERIALIZED_VIEW"
	}
	return &connections.TableMeta{
		Ref: ref, Columns: cols, Comment: str(tables[0][1]),
		Format: strings.ToLower(engine), Properties: map[string]string{"engine": engine},
	}, nil
}

func (r *reader) Close() error { return nil }
