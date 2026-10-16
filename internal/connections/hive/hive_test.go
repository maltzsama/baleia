package hive

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

func TestMetastoreAddr(t *testing.T) {
	ok := map[string]string{
		"thrift://hms:9083":       "hms:9083",
		"thrift://hms.internal:1": "hms.internal:1",
		"thrift://10.0.0.1:9083/": "10.0.0.1:9083",
	}
	for in, want := range ok {
		got, err := MetastoreAddr(in)
		if err != nil || got != want {
			t.Errorf("MetastoreAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "hms:9083", "http://hms:9083", "thrift://hms", "thrift://hms:notaport",
		"thrift://hms:0", "thrift://hms:70000", "thrift://user@hms:9083", "thrift://hms:9083/path",
	} {
		if _, err := MetastoreAddr(in); err == nil {
			t.Errorf("MetastoreAddr(%q) accepted an invalid URI", in)
		}
	}
}

func TestValidate(t *testing.T) {
	p := New(nil)
	cfg, err := p.Validate(connections.Config{Values: map[string]string{
		"metastore_uri": "thrift://hms:9083", "storage.endpoint": "http://rustfs:9000", "storage.access_key": "k",
	}, Secrets: map[string]string{"storage.secret_key": "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Value("metastore_uri") != "thrift://hms:9083" || cfg.Value("storage.type") != "s3" || cfg.Secret("storage.secret_key") != "s" {
		t.Errorf("validated config = %+v", cfg)
	}

	_, err = p.Validate(connections.Config{Values: map[string]string{"metastore_uri": "http://hms:9083"}})
	var ve *connections.ValidationError
	if !errors.As(err, &ve) || len(ve.Fields) != 1 || ve.Fields[0].Key != "metastore_uri" {
		t.Errorf("bad URI: %v", err)
	}
}

func TestPingDeniedByDialer(t *testing.T) {
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"metastore_uri": "thrift://localhost:9083"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(context.Background(), cfg); !errors.Is(err, connections.ErrDeniedNetwork) {
		t.Errorf("ping loopback = %v, want ErrDeniedNetwork", err)
	}
}

func TestPingReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"metastore_uri": "thrift://" + ln.Addr().String()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(context.Background(), cfg); err != nil {
		t.Fatalf("ping reachable metastore: %v", err)
	}
}

// OpenMetadata is unsupported: HMS metadata is read through the federated
// engine (#15).
func TestOpenMetadataUnsupported(t *testing.T) {
	if _, err := New(nil).OpenMetadata(context.Background(), connections.Config{}); !errors.Is(err, connections.ErrUnsupported) {
		t.Errorf("OpenMetadata = %v, want ErrUnsupported", err)
	}
}

// TestPingHMS runs against a real Hive Metastore (the compose `sources`
// service works: BALEIA_TEST_HIVE_METASTORE_URI=thrift://localhost:19083).
func TestPingHMS(t *testing.T) {
	uri := os.Getenv("BALEIA_TEST_HIVE_METASTORE_URI")
	if uri == "" {
		t.Skip("BALEIA_TEST_HIVE_METASTORE_URI not set")
	}
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	p := New(d)
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"metastore_uri": uri}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(context.Background(), cfg); err != nil {
		t.Fatalf("ping %s: %v", uri, err)
	}
}
