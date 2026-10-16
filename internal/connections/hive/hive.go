// Package hive is the Hive Metastore (HMS) provider: a Thrift metastore that
// holds table definitions for Iceberg, Hive and Delta Lake tables whose data
// sits in object storage. HMS executes no SQL, and Baleia reads no metadata
// from it directly in v1 (no maintained pure-Go HMS Thrift client is adopted):
// the connection is only a source to bind into a federated engine, where its
// metadata is read by introspection (docs/adr/0002, #86).
package hive

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/galileostudio/baleia/internal/connections"
)

// Provider is the HMS provider.
type Provider struct {
	dialer *connections.Dialer
}

func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindHiveMetastore }
func (*Provider) Label() string              { return "Hive Metastore" }
func (*Provider) Family() connections.Family { return connections.FamilyMetastore }

// Roles is empty: HMS is only a source to bind into a federated engine, and
// "bindable" is declared by binders (#80), never by providers. Its metadata is
// read through the engine that hosts a binding (docs/adr/0002).
func (*Provider) Roles() []connections.Role { return nil }

func (*Provider) EngineKind() string { return "" }
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "metastore_uri", Label: "Metastore URI", Type: connections.FieldText, Required: true, Help: "thrift://host:port of the Hive Metastore"},
		{Key: "storage.type", Label: "Storage type", Type: connections.FieldSelect, Options: []string{"s3"}, Default: "s3", Help: "Object storage the table data resides on (S3-compatible in v1)"},
		{Key: "storage.endpoint", Label: "S3 endpoint", Type: connections.FieldText, Help: "e.g. http://rustfs:9000; empty uses the AWS default for the region"},
		{Key: "storage.region", Label: "S3 region", Type: connections.FieldText, Help: "e.g. us-east-1"},
		{Key: "storage.path_style", Label: "Path-style access", Type: connections.FieldBool, Default: "false", Help: "Required by most S3-compatible stores (RustFS, MinIO)"},
		{Key: "storage.access_key", Label: "S3 access key", Type: connections.FieldText},
		{Key: "storage.secret_key", Label: "S3 secret key", Type: connections.FieldSecret},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	if _, err := MetastoreAddr(out.Value("metastore_uri")); err != nil {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{
			{Key: "metastore_uri", Message: err.Error()},
		}}
	}
	return out, nil
}

// MetastoreAddr returns the host:port a thrift:// HMS URI points at.
func MetastoreAddr(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "thrift" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("must be a thrift://host:port URI")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" {
		return "", fmt.Errorf("must be a thrift://host:port URI")
	}
	if err := connections.Port(port); err != nil {
		return "", fmt.Errorf("must be a thrift://host:port URI")
	}
	// The path, query and fragment of a metastore URI carry no meaning; an
	// empty path is the only accepted form.
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("must be a thrift://host:port URI")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("must be a thrift://host:port URI")
	}
	return net.JoinHostPort(host, port), nil
}

// Ping checks TCP reachability of the metastore through the dialer. A Thrift
// get_all_databases call is out of scope in v1 (#86); a bind check (#12)
// proves the engine can reach the metastore.
func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	addr, err := MetastoreAddr(cfg.Value("metastore_uri"))
	if err != nil {
		return err
	}
	conn, err := p.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// OpenMetadata is unsupported: HMS metadata is read through the federated
// engine that hosts a binding (#15), never directly.
func (*Provider) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	return nil, connections.ErrUnsupported
}
