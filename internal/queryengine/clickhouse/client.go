package clickhouse

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/version"
)

// Catalog is the one catalog of a ClickHouse server; its namespaces are the
// databases.
const Catalog = "clickhouse"

const (
	defaultHTTPPort = "8123"
	// sessionTimeout bounds an idle HTTP session on the server.
	sessionTimeout = 300
	// connectTimeout bounds a connection attempt and Ping.
	connectTimeout = 10 * time.Second
)

// emptySchema is the schema of a statement that returns no result set.
var emptySchema = arrow.NewSchema(nil, nil)

// client is one ClickHouse HTTP endpoint with credentials. Credentials travel
// in the X-ClickHouse-User / X-ClickHouse-Key headers, never in the URL.
type client struct {
	base     string // scheme://host:port/
	user     string
	password string
	database string
	http     *http.Client
}

func newClient(cfg connections.Config, d *connections.Dialer) *client {
	scheme := "http"
	if cfg.Value("secure") == "true" {
		scheme = "https"
	}
	return &client{
		base:     scheme + "://" + net.JoinHostPort(cfg.Value("host"), valueOr(cfg.Value("port"), defaultHTTPPort)) + "/",
		user:     valueOr(cfg.Value("username"), "default"),
		password: cfg.Secret("password"),
		database: valueOr(cfg.Value("database"), "default"),
		// No total timeout: streaming a result can take as long as the
		// statement's context allows.
		http: d.HTTPClient(0),
	}
}

// request is one statement to run.
type request struct {
	sql        string
	queryID    string
	sessionID  string
	readOnly   bool
	timeout    time.Duration
	logComment string
	params     map[string]string
	format     string // default: ArrowStream
}

// do sends the request. A non-200 answer is mapped from its exception code.
func (c *client) do(ctx context.Context, r request) (*http.Response, error) {
	q := url.Values{}
	q.Set("database", c.database)
	format := r.format
	if format == "" {
		format = "ArrowStream"
	}
	q.Set("default_format", format)
	// Arrow end to end: strings stay strings, no dictionary or fixed byte
	// array surprises.
	q.Set("output_format_arrow_string_as_string", "1")
	q.Set("output_format_arrow_low_cardinality_as_dictionary", "0")
	q.Set("output_format_arrow_fixed_string_as_fixed_byte_array", "0")
	if r.queryID != "" {
		q.Set("query_id", r.queryID)
	}
	if r.sessionID != "" {
		q.Set("session_id", r.sessionID)
		q.Set("session_timeout", strconv.Itoa(sessionTimeout))
	}
	if r.readOnly {
		// readonly=2 allows Baleia's own settings (max_execution_time) but
		// forbids writes and changing readonly itself.
		q.Set("readonly", "2")
	}
	if r.timeout > 0 {
		q.Set("max_execution_time", strconv.Itoa(int((r.timeout+time.Second-1)/time.Second)))
	}
	if r.logComment != "" {
		q.Set("log_comment", r.logComment)
	}
	for k, v := range r.params {
		q.Set("param_"+k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"?"+q.Encode(), strings.NewReader(r.sql))
	if err != nil {
		return nil, mapError(err)
	}
	req.Header.Set("X-ClickHouse-User", c.user)
	req.Header.Set("X-ClickHouse-Key", c.password)
	// ClickHouse records the HTTP User-Agent as http_user_agent in system.query_log.
	req.Header.Set("User-Agent", "baleia/"+version.Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, mapError(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		code, _ := strconv.Atoi(resp.Header.Get("X-ClickHouse-Exception-Code"))
		return nil, serverError(code)
	}
	return resp, nil
}

// streamArrow runs a statement and streams its Arrow batches into sink. A
// statement without a result set yields an empty body: Begin(empty) + End.
func (c *client) streamArrow(ctx context.Context, r request, sink queryengine.RecordSink, start time.Time) error {
	resp, err := c.do(ctx, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	empty, err := isEmpty(br)
	if err != nil {
		return mapError(err)
	}
	if empty {
		if err := sink.Begin(emptySchema); err != nil {
			return err
		}
		return sink.End(queryengine.Stats{ElapsedMs: time.Since(start).Milliseconds()})
	}
	reader, err := ipc.NewReader(br)
	if err != nil {
		return queryengine.NewError(queryengine.CodeInternal, Kind, "", "clickhouse arrow stream: "+err.Error())
	}
	defer reader.Release()
	if err := sink.Begin(reader.Schema()); err != nil {
		return err
	}
	var total int64
	for reader.Next() {
		rec := reader.RecordBatch()
		// The sink owns the batch and releases it; retain so it survives the
		// reader's release on the next Next().
		rec.Retain()
		if err := sink.Write(rec); err != nil {
			rec.Release()
			return err
		}
		total += rec.NumRows()
	}
	if err := reader.Err(); err != nil {
		return mapError(err)
	}
	return sink.End(queryengine.Stats{RowCount: total, ElapsedMs: time.Since(start).Milliseconds()})
}

// queryStrings runs a statement and renders every cell as text, for metadata
// reads.
func (c *client) queryStrings(ctx context.Context, r request) ([][]string, error) {
	resp, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	empty, err := isEmpty(br)
	if err != nil {
		return nil, mapError(err)
	}
	if empty {
		return nil, nil
	}
	reader, err := ipc.NewReader(br)
	if err != nil {
		return nil, queryengine.NewError(queryengine.CodeInternal, Kind, "", "clickhouse arrow stream: "+err.Error())
	}
	defer reader.Release()
	var out [][]string
	for reader.Next() {
		rec := reader.RecordBatch()
		cols := rec.Columns()
		for i := 0; i < int(rec.NumRows()); i++ {
			row := make([]string, len(cols))
			for j, col := range cols {
				if col.IsValid(i) {
					row[j] = col.ValueStr(i)
				}
			}
			out = append(out, row)
		}
	}
	if err := reader.Err(); err != nil {
		return nil, mapError(err)
	}
	return out, nil
}

// isEmpty reports whether the response body carries no Arrow stream.
func isEmpty(br *bufio.Reader) (bool, error) {
	if _, err := br.Peek(1); err != nil {
		if err == io.EOF {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
