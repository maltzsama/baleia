// Package testserver is an in-process Arrow Flight SQL server for the
// flightsql engine tests (#83). It wraps the arrow-go SQLite example server,
// which returns codes.Unknown for every SQL failure, and maps those to the
// codes the engine distinguishes (InvalidArgument, NotFound). It can also
// duplicate a statement's endpoints, to exercise a multi-endpoint read.
package testserver

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/flight"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql/example"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server is the example server with proper gRPC error codes and, optionally,
// duplicated endpoints.
type Server struct {
	*example.SQLiteFlightSQLServer
	multiEndpoint bool
}

// NewServer wraps db.
func NewServer(db *sql.DB, multiEndpoint bool) (*Server, error) {
	s, err := example.NewSQLiteFlightSQLServer(db)
	if err != nil {
		return nil, err
	}
	return &Server{SQLiteFlightSQLServer: s, multiEndpoint: multiEndpoint}, nil
}

// DoGetStatement runs the statement, mapping a SQLite failure to a gRPC code.
func (s *Server) DoGetStatement(ctx context.Context, cmd flightsql.StatementQueryTicket) (*arrow.Schema, <-chan flight.StreamChunk, error) {
	schema, ch, err := s.SQLiteFlightSQLServer.DoGetStatement(ctx, cmd)
	if err != nil {
		return nil, nil, sqlError(err)
	}
	return schema, ch, nil
}

// GetFlightInfoStatement duplicates the single endpoint when the server is in
// multi-endpoint mode.
func (s *Server) GetFlightInfoStatement(ctx context.Context, cmd flightsql.StatementQuery, desc *flight.FlightDescriptor) (*flight.FlightInfo, error) {
	info, err := s.SQLiteFlightSQLServer.GetFlightInfoStatement(ctx, cmd, desc)
	if err != nil || !s.multiEndpoint || len(info.Endpoint) == 0 {
		return info, err
	}
	info.Endpoint = append(info.Endpoint, info.Endpoint[0])
	return info, nil
}

// shutdownGrace bounds how long the shutdown function waits for streams to
// end.
const shutdownGrace = 5 * time.Second

// Start binds an in-process server on 127.0.0.1:0 and serves it, returning the
// bound address and a shutdown function.
//
// The shutdown is graceful for at most shutdownGrace. A cancelled query can
// keep running: SQLite drops an interrupt that arrives before the statement
// steps, so the conformance SleepSQL (a billion-row CTE) may run to the end,
// and GracefulStop would wait for it and hang the package (#374).
func Start(db *sql.DB, multiEndpoint bool) (string, func(), error) {
	srv, err := NewServer(db, multiEndpoint)
	if err != nil {
		return "", nil, err
	}
	server := flight.NewServerWithMiddleware(nil)
	server.RegisterFlightService(flightsql.NewFlightServer(srv))
	if err := server.Init("127.0.0.1:0"); err != nil {
		return "", nil, err
	}
	go func() { _ = server.Serve() }()
	shutdown := func() {
		done := make(chan struct{})
		go func() {
			server.Shutdown()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(shutdownGrace):
		}
	}
	return server.Addr().String(), shutdown, nil
}

// sqlError maps a SQLite error to the Flight SQL gRPC status a capable server
// would return.
func sqlError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such table"),
		strings.Contains(msg, "no such column"),
		strings.Contains(msg, "no such index"):
		return status.Error(codes.NotFound, msg)
	case strings.Contains(msg, "syntax error"),
		strings.Contains(msg, "incomplete input"),
		strings.Contains(msg, "unrecognized token"),
		strings.Contains(msg, "near \""):
		return status.Error(codes.InvalidArgument, msg)
	}
	return status.Error(codes.Internal, msg)
}
