// Package flightsql is the Arrow Flight SQL provider: any server that speaks
// Flight SQL (Dremio, DuckDB, InfluxDB, Arrow's own servers...). Metadata
// comes from the protocol's GetCatalogs, GetDBSchemas and GetTables
// commands; gRPC dials through connections.Dialer (grpc.WithContextDialer).
package flightsql

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/flight"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/galileostudio/baleia/internal/connections"
)

const (
	callTimeout = 30 * time.Second
	pageSize    = 1000
)

// DefaultCatalog stands for "no catalog" on servers that list none.
const DefaultCatalog = "default"

const (
	AuthNone  = "none"
	AuthBasic = "basic"
	AuthToken = "token"
)

type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindFlightSQL }
func (*Provider) Label() string              { return "Arrow Flight SQL" }
func (*Provider) Family() connections.Family { return connections.FamilyEngine }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleDirect}
}
func (*Provider) EngineKind() string { return "flightsql" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "host", Label: "Host", Type: connections.FieldText, Required: true},
		{Key: "port", Label: "Port", Type: connections.FieldInt, Required: true},
		{Key: "tls", Label: "TLS", Type: connections.FieldBool, Default: "true"},
		{Key: "auth", Label: "Authentication", Type: connections.FieldSelect, Options: []string{AuthNone, AuthBasic, AuthToken}, Default: AuthNone},
		{Key: "username", Label: "Username", Type: connections.FieldText, Help: "Basic authentication"},
		{Key: "password", Label: "Password", Type: connections.FieldSecret, Help: "Basic authentication"},
		{Key: "token", Label: "Bearer token", Type: connections.FieldSecret, Help: "Token authentication"},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	fail := func(key, msg string) (connections.Config, error) {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: key, Message: msg}}}
	}
	if err := connections.Port(out.Values["port"]); err != nil {
		return fail("port", err.Error())
	}
	if strings.ContainsAny(out.Values["host"], "/ ,@?#") {
		return fail("host", "must be a single host name or address")
	}
	switch out.Values["auth"] {
	case AuthBasic:
		if out.Values["username"] == "" {
			return fail("username", "required for basic authentication")
		}
		delete(out.Secrets, "token")
	case AuthToken:
		if out.Secrets["token"] == "" {
			return fail("token", "required for token authentication")
		}
		delete(out.Values, "username")
		delete(out.Secrets, "password")
	default:
		delete(out.Values, "username")
		delete(out.Secrets, "password")
		delete(out.Secrets, "token")
	}
	return out, nil
}

// conn is an open client and the outgoing metadata that authenticates it.
type conn struct {
	client *flightsql.Client
	auth   metadata.MD
}

func (c *conn) ctx(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	if len(c.auth) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, c.auth)
	}
	return ctx, cancel
}

func (p *Provider) open(ctx context.Context, cfg connections.Config) (*conn, error) {
	host := cfg.Value("host")
	creds := insecure.NewCredentials()
	if cfg.Value("tls") != "false" {
		creds = credentials.NewTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}
	client, err := flightsql.NewClientCtx(ctx, net.JoinHostPort(host, cfg.Value("port")), nil, nil,
		grpc.WithTransportCredentials(creds),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return p.dialer.DialContext(ctx, "tcp", addr)
		}))
	if err != nil {
		return nil, err
	}
	c := &conn{client: client}
	switch cfg.Value("auth") {
	case AuthToken:
		c.auth = metadata.Pairs("authorization", "Bearer "+cfg.Secret("token"))
	case AuthBasic:
		actx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		authed, err := client.Client.AuthenticateBasicToken(actx, cfg.Value("username"), cfg.Secret("password"))
		if err != nil {
			client.Close()
			return nil, mapErr(err)
		}
		c.auth, _ = metadata.FromOutgoingContext(authed)
	}
	return c, nil
}

// mapErr turns gRPC failures into the connections errors; a refused dial
// keeps its cause (ErrDeniedNetwork) so callers can tell it apart.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, connections.ErrDeniedNetwork) {
		return err
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	if strings.Contains(st.Message(), connections.ErrDeniedNetwork.Error()) {
		return fmt.Errorf("%w: %s", connections.ErrDeniedNetwork, st.Message())
	}
	switch st.Code() {
	case codes.NotFound:
		return fmt.Errorf("%w: %s", connections.ErrNotFound, st.Message())
	case codes.Unauthenticated, codes.PermissionDenied:
		return fmt.Errorf("flight sql: %s", st.Code())
	}
	return fmt.Errorf("flight sql: %s: %s", st.Code(), st.Message())
}

// Ping authenticates and lists catalogs: the cheapest call every server has.
func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	c, err := p.open(ctx, cfg)
	if err != nil {
		return mapErr(err)
	}
	defer c.client.Close()
	_, err = c.catalogs(ctx)
	return err
}

func (p *Provider) OpenMetadata(ctx context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	c, err := p.open(ctx, cfg)
	if err != nil {
		return nil, mapErr(err)
	}
	if _, err := c.catalogs(ctx); err != nil {
		c.client.Close()
		return nil, err
	}
	return &reader{c: c}, nil
}

// fetch runs a metadata command and calls each for every record of every
// endpoint.
func (c *conn) fetch(ctx context.Context, info func(context.Context) (*flight.FlightInfo, error), each func(arrow.RecordBatch) error) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	fi, err := info(ctx)
	if err != nil {
		return mapErr(err)
	}
	for _, ep := range fi.Endpoint {
		rdr, err := c.client.DoGet(ctx, ep.Ticket)
		if err != nil {
			return mapErr(err)
		}
		for rdr.Next() {
			if err := each(rdr.RecordBatch()); err != nil {
				rdr.Release()
				return err
			}
		}
		err = rdr.Err()
		rdr.Release()
		if err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// column returns the column called name of rec as Go strings ("" for nulls).
func column(rec arrow.RecordBatch, name string) []string {
	idx := rec.Schema().FieldIndices(name)
	if len(idx) == 0 {
		return make([]string, rec.NumRows())
	}
	col, ok := rec.Column(idx[0]).(*array.String)
	out := make([]string, rec.NumRows())
	if !ok {
		return out
	}
	for i := range out {
		if col.IsValid(i) {
			out[i] = col.Value(i)
		}
	}
	return out
}

func (c *conn) catalogs(ctx context.Context) ([]string, error) {
	var out []string
	err := c.fetch(ctx, func(ctx context.Context) (*flight.FlightInfo, error) { return c.client.GetCatalogs(ctx) },
		func(rec arrow.RecordBatch) error {
			out = append(out, column(rec, "catalog_name")...)
			return nil
		})
	return out, err
}

type reader struct{ c *conn }

// catalogFilter maps a Baleia catalog to the protocol filter: the default
// catalog of a server without catalogs is "no filter".
func catalogFilter(catalog string) *string {
	if catalog == DefaultCatalog {
		return nil
	}
	return &catalog
}

func (r *reader) ListCatalogs(ctx context.Context) ([]connections.CatalogRef, error) {
	names, err := r.c.catalogs(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return []connections.CatalogRef{{Name: DefaultCatalog, Kind: "flightsql"}}, nil
	}
	out := make([]connections.CatalogRef, 0, len(names))
	for _, n := range names {
		out = append(out, connections.CatalogRef{Name: n, Kind: "flightsql"})
	}
	return out, nil
}

func (r *reader) ListNamespaces(ctx context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	if len(parent) > 0 {
		return []connections.Namespace{}, nil
	}
	var names []string
	err := r.c.fetch(ctx, func(ctx context.Context) (*flight.FlightInfo, error) {
		return r.c.client.GetDBSchemas(ctx, &flightsql.GetDBSchemasOpts{Catalog: catalogFilter(catalog)})
	}, func(rec arrow.RecordBatch) error {
		names = append(names, column(rec, "db_schema_name")...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A server without schemas has its tables in one unnamed namespace.
	if len(names) == 0 {
		return []connections.Namespace{{}}, nil
	}
	slices.Sort(names)
	out := make([]connections.Namespace, 0, len(names))
	for _, n := range slices.Compact(names) {
		out = append(out, connections.Namespace{n})
	}
	return out, nil
}

// schemaOf is the protocol's schema of a namespace ("" for the unnamed one).
func schemaOf(ns connections.Namespace) (string, error) {
	switch len(ns) {
	case 0:
		return "", nil
	case 1:
		return ns[0], nil
	}
	return "", fmt.Errorf("%w: namespace %v", connections.ErrNotFound, ns)
}

type table struct {
	name, kind string
	schema     []byte
}

// tables lists the tables of one schema. Filter patterns use LIKE wildcards
// without an escape, so names are matched exactly here as well.
func (r *reader) tables(ctx context.Context, catalog string, ns connections.Namespace, name *string, withSchema bool) ([]table, error) {
	schema, err := schemaOf(ns)
	if err != nil {
		return nil, err
	}
	opts := &flightsql.GetTablesOpts{Catalog: catalogFilter(catalog), TableNameFilterPattern: name, IncludeSchema: withSchema}
	if schema != "" {
		opts.DbSchemaFilterPattern = &schema
	}
	var out []table
	err = r.c.fetch(ctx, func(ctx context.Context) (*flight.FlightInfo, error) {
		return r.c.client.GetTables(ctx, opts)
	}, func(rec arrow.RecordBatch) error {
		schemas, names, kinds := column(rec, "db_schema_name"), column(rec, "table_name"), column(rec, "table_type")
		var blobs *array.Binary
		if idx := rec.Schema().FieldIndices("table_schema"); withSchema && len(idx) > 0 {
			blobs, _ = rec.Column(idx[0]).(*array.Binary)
		}
		for i := range names {
			if schemas[i] != schema || (name != nil && names[i] != *name) {
				continue
			}
			t := table{name: names[i], kind: kinds[i]}
			if blobs != nil && blobs.IsValid(i) {
				t.schema = slices.Clone(blobs.Value(i))
			}
			out = append(out, t)
		}
		return nil
	})
	return out, err
}

func refType(kind string) string {
	if strings.Contains(strings.ToUpper(kind), "VIEW") {
		return "VIEW"
	}
	return "TABLE"
}

func (r *reader) ListTables(ctx context.Context, catalog string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	all, err := r.tables(ctx, catalog, ns, nil, false)
	if err != nil {
		return nil, "", err
	}
	slices.SortFunc(all, func(a, b table) int { return strings.Compare(a.name, b.name) })
	out := []connections.TableRef{}
	for _, t := range all {
		if t.name > pageToken {
			out = append(out, connections.TableRef{Catalog: catalog, Namespace: ns, Name: t.name, Type: refType(t.kind)})
		}
	}
	next := ""
	if len(out) > pageSize {
		out = out[:pageSize]
		next = out[pageSize-1].Name
	}
	return out, next, nil
}

func (r *reader) LoadTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	name := t.Name
	found, err := r.tables(ctx, t.Catalog, t.Namespace, &name, true)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w: table", connections.ErrNotFound)
	}
	ref := t
	ref.Type = refType(found[0].kind)
	meta := &connections.TableMeta{Ref: ref, Columns: []connections.Column{}, Format: "flightsql", Properties: map[string]string{"table_type": found[0].kind}}
	if found[0].schema == nil {
		return meta, nil
	}
	schema, err := flight.DeserializeSchema(found[0].schema, memory.DefaultAllocator)
	if err != nil {
		return nil, fmt.Errorf("flight sql table schema: %w", err)
	}
	for i, f := range schema.Fields() {
		native := f.Type.String()
		md := flightsql.ColumnMetadata{Data: &f.Metadata}
		if tn, ok := md.TypeName(); ok && tn != "" {
			native = tn
		}
		meta.Columns = append(meta.Columns, connections.Column{Name: f.Name, NativeType: native, Nullable: f.Nullable, Position: i + 1})
	}
	return meta, nil
}

func (r *reader) Close() error { return r.c.client.Close() }
