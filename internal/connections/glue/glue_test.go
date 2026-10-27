package glue

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/conntest"
)

// TestNoAmbientCredentials: a connection with neither keys nor a role is
// rejected unless the operator allowed ambient credentials (#86).
func TestNoAmbientCredentials(t *testing.T) {
	t.Parallel()
	off, on := New(nil, Options{}), New(nil, Options{AllowAmbientCredentials: true})
	base := func() connections.Config {
		return connections.Config{Values: map[string]string{"region": "us-east-1"}}
	}
	_, err := off.Validate(base())
	var ve *connections.ValidationError
	if !errors.As(err, &ve) || len(ve.Fields) != 1 || ve.Fields[0].Key != "access_key" {
		t.Errorf("ambient credentials accepted: %v", err)
	}
	if _, err := on.Validate(base()); err != nil {
		t.Errorf("operator flag ignored: %v", err)
	}
	// A role alone still needs base credentials (static keys or ambient).
	role := base()
	role.Values["role_arn"] = "arn:aws:iam::1:role/baleia"
	if _, err := off.Validate(role); !errors.As(err, &ve) {
		t.Errorf("role without base credentials accepted: %v", err)
	}
	// Static keys, with or without a role, need no flag.
	withKeys := base()
	withKeys.Values["access_key"], withKeys.Secrets = "k", map[string]string{"secret_key": "s"}
	withKeys.Values["role_arn"] = "arn:aws:iam::1:role/baleia"
	if _, err := off.Validate(withKeys); err != nil {
		t.Errorf("keys and role rejected: %v", err)
	}
	// access_key and secret_key go together.
	half := base()
	half.Values["access_key"] = "k"
	if _, err := off.Validate(half); !errors.As(err, &ve) {
		t.Errorf("half a key pair accepted: %v", err)
	}
}

func TestValidateEndpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		endpoint string
		secure   bool
		ok       bool
	}{
		{"https://glue.eu-west-1.amazonaws.com", false, true},
		{"http://localhost:5000", false, false},
		{"http://localhost:5000", true, true},
		{"ftp://glue", true, false},
		{"https://user:pass@glue", true, false},
	}
	for _, c := range cases {
		p := New(nil, Options{AllowAmbientCredentials: true, AllowInsecureEndpoint: c.secure})
		_, err := p.Validate(connections.Config{Values: map[string]string{"region": "us-east-1", "endpoint": c.endpoint}})
		if (err == nil) != c.ok {
			t.Errorf("endpoint %q secure=%v: %v", c.endpoint, c.secure, err)
		}
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		params map[string]string
		want   string
	}{
		{map[string]string{"table_type": "ICEBERG"}, "iceberg"},
		{map[string]string{"table_type": "HIVE"}, "hive"},
		{map[string]string{"table_type": "DELTA"}, "delta"},
		{map[string]string{"spark.sql.sources.provider": "delta"}, "delta"},
		{map[string]string{"metadata_location": "s3://b/t/metadata/1.json"}, "iceberg"},
		{map[string]string{}, "hive"},
	}
	for _, c := range cases {
		if got := Format(gluetypes.Table{Parameters: c.params}); got != c.want {
			t.Errorf("%v: %q, want %q", c.params, got, c.want)
		}
	}
}

// TestMetadataReader_Moto reads a real Glue API backed by moto. Run it with a
// moto server: docker run -p 5000:5000 motoserver/moto, then
// BALEIA_TEST_MOTO_URL=http://localhost:5000 go test ./internal/connections/glue/
func TestMetadataReader_Moto(t *testing.T) {
	t.Parallel()
	endpoint := os.Getenv("BALEIA_TEST_MOTO_URL")
	if endpoint == "" {
		t.Skip("BALEIA_TEST_MOTO_URL not set")
	}
	ctx := context.Background()
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d, Options{AllowInsecureEndpoint: true})
	cfg, err := p.Validate(connections.Config{Values: map[string]string{
		"region": "us-east-1", "endpoint": endpoint, "access_key": "test",
	}, Secrets: map[string]string{"secret_key": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(ctx, cfg); err != nil {
		t.Fatalf("ping: %v", err)
	}

	c, err := p.client(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, c, "analytics")
	seedTable(t, c, "analytics", "events", map[string]string{"table_type": "ICEBERG", "metadata_location": "s3://b/events/metadata/1.json"},
		gluetypes.Column{Name: aws.String("id"), Type: aws.String("bigint")},
		gluetypes.Column{Name: aws.String("tags"), Type: aws.String("array<string>")})
	seedTable(t, c, "analytics", "customers", map[string]string{"table_type": "HIVE"},
		gluetypes.Column{Name: aws.String("name"), Type: aws.String("string")})

	r, err := p.OpenMetadata(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	cats, err := r.ListCatalogs(ctx)
	if err != nil || len(cats) != 1 || cats[0].Name != Catalog {
		t.Fatalf("catalogs = %v, %v", cats, err)
	}
	nss, err := r.ListNamespaces(ctx, Catalog, nil)
	if err != nil || len(nss) != 1 || len(nss[0]) != 1 || nss[0][0] != "analytics" {
		t.Fatalf("namespaces = %v, %v", nss, err)
	}
	refs, next, err := r.ListTables(ctx, Catalog, connections.Namespace{"analytics"}, "")
	if err != nil || next != "" || len(refs) != 2 {
		t.Fatalf("tables = %v %q, %v", refs, next, err)
	}
	m, err := r.LoadTable(ctx, connections.TableRef{Catalog: Catalog, Namespace: connections.Namespace{"analytics"}, Name: "events"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != "iceberg" || len(m.Columns) != 2 || m.Columns[0].Name != "id" || m.Columns[1].NativeType != "array<string>" {
		t.Errorf("events = %+v", m)
	}
	if m.Properties["metadata_location"] != "s3://b/events/metadata/1.json" {
		t.Errorf("events properties = %v", m.Properties)
	}

	conntest.CheckWalk(t, r, Catalog,
		connections.TableRef{Catalog: Catalog, Namespace: connections.Namespace{"analytics"}, Name: "customers", Type: "TABLE"},
		connections.TableRef{Catalog: Catalog, Namespace: connections.Namespace{"analytics"}, Name: "events", Type: "TABLE"})
}

func seed(t *testing.T, c *glue.Client, db string) {
	t.Helper()
	ctx := context.Background()
	_, _ = c.DeleteDatabase(ctx, &glue.DeleteDatabaseInput{Name: aws.String(db)}) // idempotent re-runs
	if _, err := c.CreateDatabase(ctx, &glue.CreateDatabaseInput{
		DatabaseInput: &gluetypes.DatabaseInput{Name: aws.String(db)},
	}); err != nil {
		t.Fatalf("create database %s: %v", db, err)
	}
}

func seedTable(t *testing.T, c *glue.Client, db, name string, params map[string]string, cols ...gluetypes.Column) {
	t.Helper()
	ctx := context.Background()
	_, _ = c.DeleteTable(ctx, &glue.DeleteTableInput{DatabaseName: aws.String(db), Name: aws.String(name)})
	ti := &gluetypes.TableInput{
		Name:       aws.String(name),
		TableType:  aws.String("EXTERNAL_TABLE"),
		Parameters: params,
		StorageDescriptor: &gluetypes.StorageDescriptor{
			Location: aws.String("s3://b/" + name),
			Columns:  cols,
		},
	}
	if _, err := c.CreateTable(ctx, &glue.CreateTableInput{DatabaseName: aws.String(db), TableInput: ti}); err != nil {
		t.Fatalf("create table %s.%s: %v", db, name, err)
	}
}
