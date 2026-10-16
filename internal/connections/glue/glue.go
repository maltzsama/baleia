// Package glue is the AWS Glue Data Catalog provider: a metastore that holds
// table definitions for Iceberg, Hive and Delta Lake tables whose data sits in
// object storage. Unlike a Hive Metastore, Glue serves its metadata over the
// AWS API, so Baleia reads it directly (RoleMetadata) as well as binding it
// into a federated engine (#86).
//
// No ambient credentials: the AWS SDK never falls back to the host's default
// credential chain (which would let any connection owner use the platform's
// IAM role) unless the operator sets BALEIA_ALLOW_AMBIENT_AWS_CREDENTIALS.
// Every AWS call goes through connections.Dialer (docs/adr/0011).
package glue

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/galileostudio/baleia/internal/connections"
)

// Catalog is the one logical catalog of a Glue connection: the whole Data
// Catalog, whose databases are schemas/namespaces.
const Catalog = "glue"

const requestTimeout = 30 * time.Second

// Options are the server-side Glue settings.
type Options struct {
	// AllowAmbientCredentials lets a connection with no static keys use the
	// host's default AWS credential chain (to call Glue, or to assume a role)
	// — BALEIA_ALLOW_AMBIENT_AWS_CREDENTIALS. Off by default: any connection
	// owner could otherwise use the platform's IAM role.
	AllowAmbientCredentials bool
	// AllowInsecureEndpoint allows an http:// endpoint override, for local
	// emulators (moto). Off in production.
	AllowInsecureEndpoint bool
}

type Provider struct {
	dialer *connections.Dialer
	opts   Options
}

func New(d *connections.Dialer, o Options) *Provider { return &Provider{dialer: d, opts: o} }

func (*Provider) Kind() connections.Kind     { return connections.KindGlue }
func (*Provider) Label() string              { return "AWS Glue Data Catalog" }
func (*Provider) Family() connections.Family { return connections.FamilyMetastore }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata}
}
func (*Provider) EngineKind() string { return "" }

// WalksCatalogs: the reader reads the whole Data Catalog in the walk (#15).
func (*Provider) WalksCatalogs() bool { return true }

var _ connections.CatalogWalker = (*reader)(nil)

func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "region", Label: "Region", Type: connections.FieldText, Required: true, Help: "AWS region of the Data Catalog, e.g. us-east-1"},
		{Key: "catalog_id", Label: "Catalog ID", Type: connections.FieldText, Help: "Cross-account Data Catalog ID; empty uses the caller's account"},
		{Key: "endpoint", Label: "API endpoint", Type: connections.FieldText, Help: "Override for tests or regional endpoints, e.g. http://moto:5000; empty uses the AWS default"},
		{Key: "access_key", Label: "Access key ID", Type: connections.FieldText, Help: "Static credentials; when empty the operator must allow ambient credentials (a role ARN alone still needs base credentials)"},
		{Key: "secret_key", Label: "Secret access key", Type: connections.FieldSecret},
		{Key: "role_arn", Label: "Role ARN", Type: connections.FieldText, Help: "Assume this role (with the keys, or the ambient chain) for Glue and S3"},
		{Key: "external_id", Label: "External ID", Type: connections.FieldText, Help: "External ID of the assumed role's trust policy"},
		{Key: "storage.type", Label: "Storage type", Type: connections.FieldSelect, Options: []string{"s3"}, Default: "s3", Help: "Object storage the table data resides on (S3-compatible in v1)"},
		{Key: "storage.endpoint", Label: "S3 endpoint", Type: connections.FieldText, Help: "empty uses the AWS default for the region"},
		{Key: "storage.region", Label: "S3 region", Type: connections.FieldText},
		{Key: "storage.path_style", Label: "Path-style access", Type: connections.FieldBool, Default: "false", Help: "Required by most S3-compatible stores (RustFS, MinIO)"},
		{Key: "storage.access_key", Label: "S3 access key", Type: connections.FieldText, Help: "Only when the table data is not on AWS S3 with the Glue credentials"},
		{Key: "storage.secret_key", Label: "S3 secret key", Type: connections.FieldSecret},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	accessKey, secretKey, role := out.Value("access_key"), out.Secret("secret_key"), out.Value("role_arn")
	var errs []connections.FieldError
	if (accessKey == "") != (secretKey == "") {
		errs = append(errs, connections.FieldError{Key: "secret_key", Message: "access_key and secret_key go together"})
	}
	// No ambient credentials unless the operator allows them: without static
	// keys the SDK would resolve the host's default chain (to call the Glue
	// API, or to assume role_arn), letting any connection owner use the
	// platform's IAM role.
	if accessKey == "" && !p.opts.AllowAmbientCredentials {
		msg := "provide access_key/secret_key; a role_arn alone still needs base credentials"
		if role == "" {
			msg = "provide access_key/secret_key or role_arn"
		}
		errs = append(errs, connections.FieldError{Key: "access_key", Message: msg + "; ambient AWS credentials are disabled (BALEIA_ALLOW_AMBIENT_AWS_CREDENTIALS)"})
	}
	if ep := out.Value("endpoint"); ep != "" {
		if u, err := url.Parse(ep); err != nil || u.Host == "" || u.User != nil ||
			(u.Scheme != "https" && !(u.Scheme == "http" && p.opts.AllowInsecureEndpoint)) {
			errs = append(errs, connections.FieldError{Key: "endpoint", Message: "must be an https URL (http only when insecure endpoints are allowed for development)"})
		}
	}
	if len(errs) > 0 {
		return connections.Config{}, &connections.ValidationError{Fields: errs}
	}
	return out, nil
}

// awsConfig builds the SDK configuration of a connection. Credentials are
// always explicit unless the operator allowed ambient ones, so the SDK never
// silently assumes the host's role.
func (p *Provider) awsConfig(ctx context.Context, cfg connections.Config) (aws.Config, error) {
	hc := p.dialer.HTTPClient(requestTimeout)
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Value("region")),
		awsconfig.WithHTTPClient(hc),
	}
	if ak := cfg.Value("access_key"); ak != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(ak, cfg.Secret("secret_key"), ""))))
	}
	ac, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}
	if ep := cfg.Value("endpoint"); ep != "" {
		ac.BaseEndpoint = aws.String(ep)
	}
	if role := cfg.Value("role_arn"); role != "" {
		// STS uses the SDK's own endpoint: only the Glue (and S3) calls are
		// pointed at an override.
		stsCfg := ac
		stsCfg.BaseEndpoint = nil
		stsClient := sts.NewFromConfig(stsCfg, func(o *sts.Options) { o.HTTPClient = hc })
		ac.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(stsClient, role, func(o *stscreds.AssumeRoleOptions) {
			if ext := cfg.Value("external_id"); ext != "" {
				o.ExternalID = aws.String(ext)
			}
		}))
	}
	return ac, nil
}

func (p *Provider) client(ctx context.Context, cfg connections.Config) (*glue.Client, error) {
	ac, err := p.awsConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return glue.NewFromConfig(ac), nil
}

func (p *Provider) OpenMetadata(ctx context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	c, err := p.client(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &reader{client: c, catalogID: catalogID(cfg)}, nil
}

// catalogID is the cross-account Data Catalog ID, or nil for the caller's.
func catalogID(cfg connections.Config) *string {
	if id := cfg.Value("catalog_id"); id != "" {
		return aws.String(id)
	}
	return nil
}

// Ping lists one database: it proves reachability and that the credentials
// (and the assumed role) are accepted.
func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	c, err := p.client(ctx, cfg)
	if err != nil {
		return err
	}
	if _, err := c.GetDatabases(ctx, &glue.GetDatabasesInput{MaxResults: aws.Int32(1), CatalogId: catalogID(cfg)}); err != nil {
		return err
	}
	return nil
}

// reader reads the Data Catalog. One Baleia catalog is the whole metastore;
// its databases are namespaces and its tables carry a format property.
type reader struct {
	client    *glue.Client
	catalogID *string
}

func (*reader) Close() error { return nil }

func (*reader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	return []connections.CatalogRef{{Name: Catalog, Kind: "glue"}}, nil
}

func (r *reader) ListNamespaces(ctx context.Context, _ string, parent connections.Namespace) ([]connections.Namespace, error) {
	if len(parent) > 0 {
		return nil, nil // Glue databases have no sub-namespaces
	}
	dbs, err := r.databases(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]connections.Namespace, 0, len(dbs))
	for _, db := range dbs {
		out = append(out, connections.Namespace{db})
	}
	return out, nil
}

func (r *reader) ListTables(ctx context.Context, _ string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	if len(ns) == 0 {
		return nil, "", nil
	}
	in := &glue.GetTablesInput{DatabaseName: aws.String(ns[0]), CatalogId: r.catalogID}
	if pageToken != "" {
		in.NextToken = aws.String(pageToken)
	}
	out, err := r.client.GetTables(ctx, in)
	if err != nil {
		return nil, "", err
	}
	refs := make([]connections.TableRef, 0, len(out.TableList))
	for _, t := range out.TableList {
		refs = append(refs, ref(ns[0], t))
	}
	next := ""
	if out.NextToken != nil {
		next = *out.NextToken
	}
	return refs, next, nil
}

func (r *reader) LoadTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	db := ""
	if len(t.Namespace) > 0 {
		db = t.Namespace[0]
	}
	out, err := r.client.GetTable(ctx, &glue.GetTableInput{DatabaseName: aws.String(db), Name: aws.String(t.Name), CatalogId: r.catalogID})
	if err != nil {
		var nf *gluetypes.EntityNotFoundException
		if errors.As(err, &nf) {
			return nil, connections.ErrNotFound
		}
		return nil, err
	}
	m := meta(db, out.Table)
	return &m, nil
}

// WalkCatalog streams every table of the metastore, ordered by database then
// name, one database at a time.
func (r *reader) WalkCatalog(ctx context.Context, _ string, visit func(connections.TableMeta) error) error {
	dbs, err := r.databases(ctx)
	if err != nil {
		return err
	}
	for _, db := range dbs {
		tables, err := r.allTables(ctx, db)
		if err != nil {
			return err
		}
		for i := range tables {
			if err := visit(meta(db, &tables[i])); err != nil {
				return err
			}
		}
	}
	return nil
}

// databases lists every database name, sorted.
func (r *reader) databases(ctx context.Context) ([]string, error) {
	var out []string
	var token *string
	for {
		page, err := r.client.GetDatabases(ctx, &glue.GetDatabasesInput{NextToken: token, CatalogId: r.catalogID})
		if err != nil {
			return nil, err
		}
		for _, db := range page.DatabaseList {
			if db.Name != nil && *db.Name != "" {
				out = append(out, *db.Name)
			}
		}
		if page.NextToken == nil || *page.NextToken == "" {
			break
		}
		token = page.NextToken
	}
	sort.Strings(out)
	return out, nil
}

// allTables reads every table of a database, sorted by name.
func (r *reader) allTables(ctx context.Context, db string) ([]gluetypes.Table, error) {
	var out []gluetypes.Table
	var token *string
	for {
		page, err := r.client.GetTables(ctx, &glue.GetTablesInput{DatabaseName: aws.String(db), NextToken: token, CatalogId: r.catalogID})
		if err != nil {
			return nil, err
		}
		out = append(out, page.TableList...)
		if page.NextToken == nil || *page.NextToken == "" {
			break
		}
		token = page.NextToken
	}
	sort.Slice(out, func(i, j int) bool { return aws.ToString(out[i].Name) < aws.ToString(out[j].Name) })
	return out, nil
}

func ref(db string, t gluetypes.Table) connections.TableRef {
	name := aws.ToString(t.Name)
	return connections.TableRef{
		Catalog:   Catalog,
		Namespace: connections.Namespace{db},
		Name:      name,
		Type:      tableType(t),
	}
}

func tableType(t gluetypes.Table) string {
	switch strings.ToUpper(aws.ToString(t.TableType)) {
	case "VIRTUAL_VIEW":
		return "VIEW"
	default:
		return "TABLE"
	}
}

// Format derives the table format from the Glue table parameters, the way
// Trino's connectors recognize it.
func Format(t gluetypes.Table) string {
	p := t.Parameters
	switch strings.ToLower(p["table_type"]) {
	case "iceberg":
		return "iceberg"
	case "delta":
		return "delta"
	case "hive":
		return "hive"
	}
	if strings.EqualFold(p["spark.sql.sources.provider"], "delta") {
		return "delta"
	}
	if p["metadata_location"] != "" || p["format-version"] != "" {
		return "iceberg"
	}
	return "hive"
}

func meta(db string, t *gluetypes.Table) connections.TableMeta {
	name := aws.ToString(t.Name)
	props := map[string]string{}
	if t.Parameters != nil {
		for k, v := range t.Parameters {
			props[k] = v
		}
	}
	if t.StorageDescriptor != nil && t.StorageDescriptor.Location != nil {
		props["location"] = *t.StorageDescriptor.Location
	}
	format := Format(*t)
	props["format"] = format
	return connections.TableMeta{
		Ref:        connections.TableRef{Catalog: Catalog, Namespace: connections.Namespace{db}, Name: name, Type: tableType(*t)},
		Columns:    columns(t),
		Comment:    comment(*t),
		Format:     format,
		Properties: props,
	}
}

func columns(t *gluetypes.Table) []connections.Column {
	var storage []gluetypes.Column
	if t.StorageDescriptor != nil {
		storage = t.StorageDescriptor.Columns
	}
	out := make([]connections.Column, 0, len(storage)+len(t.PartitionKeys))
	for _, c := range storage {
		out = append(out, column(c, len(out)+1))
	}
	for _, c := range t.PartitionKeys {
		out = append(out, column(c, len(out)+1))
	}
	return out
}

func column(c gluetypes.Column, pos int) connections.Column {
	return connections.Column{
		Name:       aws.ToString(c.Name),
		NativeType: aws.ToString(c.Type),
		Nullable:   true, // Glue columns carry no nullability
		Comment:    aws.ToString(c.Comment),
		Position:   pos,
	}
}

func comment(t gluetypes.Table) string {
	if t.Parameters != nil {
		if c := t.Parameters["comment"]; c != "" {
			return c
		}
	}
	if t.Description != nil {
		return *t.Description
	}
	return ""
}
