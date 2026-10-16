// Package flightsql is the Arrow Flight SQL direct engine (#83): one adapter
// reaches any Flight SQL server (Dremio, DuckDB, InfluxDB, Doris/StarRocks,
// …). Flight SQL is a protocol, not a database, so the dialect is the ANSI
// subset and every server difference is declared, not discovered. Results
// arrive as Arrow natively and stream straight into the sink.
//
// Credentials come from the connection and never reach the URL; every dial goes
// through connections.Dialer (docs/adr/0011).
package flightsql

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/flight"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql"
	"github.com/galileostudio/baleia/internal/connections"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// callTimeout bounds one metadata call; statements use the caller's context.
const callTimeout = 30 * time.Second

// DefaultCatalog stands for "no catalog" on servers that list none.
const DefaultCatalog = "default"

// Authentication modes of a connection.
const (
	AuthNone  = "none"
	AuthBasic = "basic"
	AuthToken = "token"
)

// client is an open Flight SQL client and the outgoing metadata that
// authenticates it.
type client struct {
	client *flightsql.Client
	auth   metadata.MD
}

// openClient dials and authenticates a Flight SQL client through d.
func openClient(ctx context.Context, cfg connections.Config, d *connections.Dialer) (*client, error) {
	host := cfg.Value("host")
	creds := insecure.NewCredentials()
	if cfg.Value("tls") != "false" {
		creds = credentials.NewTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}
	fc, err := flightsql.NewClientCtx(ctx, net.JoinHostPort(host, cfg.Value("port")), nil, nil,
		grpc.WithTransportCredentials(creds),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp", addr)
		}))
	if err != nil {
		return nil, err
	}
	c := &client{client: fc}
	switch cfg.Value("auth") {
	case AuthToken:
		c.auth = metadata.Pairs("authorization", "Bearer "+cfg.Secret("token"))
	case AuthBasic:
		actx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		authed, err := fc.Client.AuthenticateBasicToken(actx, cfg.Value("username"), cfg.Secret("password"))
		if err != nil {
			fc.Close()
			return nil, err
		}
		c.auth, _ = metadata.FromOutgoingContext(authed)
	}
	return c, nil
}

func (c *client) close() error { return c.client.Close() }

// withAuth attaches the credentials to an outgoing context.
func (c *client) withAuth(ctx context.Context) context.Context {
	if len(c.auth) == 0 {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, c.auth)
}

// fetch runs a metadata command and calls each for every record of every
// endpoint, in order.
func (c *client) fetch(ctx context.Context, info func(context.Context) (*flight.FlightInfo, error), each func(arrow.RecordBatch) error) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	ctx = c.withAuth(ctx)
	fi, err := info(ctx)
	if err != nil {
		return mapError(err)
	}
	for _, ep := range fi.Endpoint {
		rdr, err := c.client.DoGet(ctx, ep.Ticket)
		if err != nil {
			return mapError(err)
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
			return mapError(err)
		}
	}
	return nil
}

// column returns the column called name of rec as Go strings ("" for nulls).
// Servers send string, large_string or string_view.
func column(rec arrow.RecordBatch, name string) []string {
	idx := rec.Schema().FieldIndices(name)
	if len(idx) == 0 {
		return make([]string, rec.NumRows())
	}
	col := rec.Column(idx[0])
	out := make([]string, rec.NumRows())
	for i := range out {
		if col.IsValid(i) {
			out[i] = col.ValueStr(i)
		}
	}
	return out
}

// binaryColumn is a binary or large_binary column.
type binaryColumn interface {
	IsValid(i int) bool
	Value(i int) []byte
}
