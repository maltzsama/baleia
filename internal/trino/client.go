package trino

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	user    string
	http    *http.Client
}

func New(baseURL, user string) *Client {
	return NewWithHTTPClient(baseURL, user, &http.Client{Timeout: 60 * time.Second})
}

// NewWithHTTPClient is New with the caller's HTTP client, e.g. one that dials
// through connections.Dialer (docs/adr/0011).
func NewWithHTTPClient(baseURL, user string, hc *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), user: user, http: hc}
}

// QueryError is a failure Trino reported for a statement. ErrorName and
// ErrorType are Trino's own classification (e.g. SYNTAX_ERROR, USER_ERROR).
type QueryError struct {
	QueryID   string
	Message   string
	ErrorName string
	ErrorType string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("trino error: %s (%s)", e.Message, e.ErrorName)
}

// TransportError means Trino could not be reached or answered with an HTTP
// status other than 2xx instead of a statement response.
type TransportError struct {
	Status int // 0 for a network error
	Err    error
}

func (e *TransportError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("trino unavailable: HTTP %d", e.Status)
	}
	return "trino unavailable: " + e.Err.Error()
}

func (e *TransportError) Unwrap() error { return e.Err }

// Code is the error's machine-readable code (bindings.ErrorCode).
func (e *TransportError) Code() string { return "engine_unavailable" }

// Code is Trino's error name, lower-cased (bindings.ErrorCode).
func (e *QueryError) Code() string { return strings.ToLower(e.ErrorName) }

func queryError(id string, se *StatementError) *QueryError {
	return &QueryError{QueryID: id, Message: se.Message, ErrorName: se.ErrorName, ErrorType: se.ErrorType}
}

// ServerInfo is the answer of GET /v1/info.
type ServerInfo struct {
	NodeVersion struct {
		Version string `json:"version"`
	} `json:"nodeVersion"`
	Coordinator bool `json:"coordinator"`
	Starting    bool `json:"starting"`
}

// Info returns the coordinator's /v1/info.
func (c *Client) Info(ctx context.Context) (*ServerInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &TransportError{Status: resp.StatusCode}
	}
	var info ServerInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, &TransportError{Err: err}
	}
	return &info, nil
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type StatementStats struct {
	State     string `json:"state"`
	Rows      int64  `json:"processedRows"`
	Bytes     int64  `json:"processedBytes"`
	ElapsedMs int64  `json:"elapsedTimeMillis"`
}

type StatementResponse struct {
	ID      string          `json:"id"`
	InfoURI string          `json:"infoUri"`
	NextURI string          `json:"nextUri"`
	Stats   StatementStats  `json:"stats"`
	Columns []Column        `json:"columns"`
	Data    [][]any         `json:"data"`
	Error   *StatementError `json:"error"`
}

type StatementError struct {
	Message   string `json:"message"`
	ErrorCode any    `json:"errorCode"`
	ErrorName string `json:"errorName"`
	ErrorType string `json:"errorType"`
}

type QueryResult struct {
	QueryID  string
	Columns  []Column
	Data     [][]any
	RowCount int64
}

// CatalogSpec describes a catalog to register in Trino at runtime via
// CREATE CATALOG (requires catalog.management=dynamic). DDL is generated
// from validated fields — see buildCreateCatalogDDL — and never accepts
// bind parameters, so validation is the only defense against injection.
//
// Secrets handling:
//   - Properties carries REAL values (these are written to the registry).
//   - SecretKeys lists property keys whose value must be replaced with the
//     @baleia-secret[<Name>:<key>] placeholder in the DDL text. The
//     placeholder is resolved by the baleia-trino-catalog-store plugin's
//     SecretResolver at createCatalogProperties() time, by reading the
//     real value back from trino_catalog_registry.
//
// "already exists" with an identical version is treated as success (the
// catalog is already registered with the right config); "already exists"
// with a DIVERGENT version or hash is surfaced as ErrCatalogExists.
// Callers must DROP then CREATE in that case; there is no ALTER CATALOG
// in Trino OSS (CR-014 §8.4).
type CatalogSpec struct {
	Name       string
	Connector  string            // iceberg, postgresql, mysql, etc.
	Properties map[string]string // REAL values; secrets in cleartext
	SecretKeys []string          // keys that become @baleia-secret[...] in DDL
}

// ErrCatalogExists is returned by CreateCatalog when the catalog name
// is already registered with a DIFFERENT configuration. Callers must
// DropCatalog then CreateCatalog again; there is no ALTER CATALOG.
var ErrCatalogExists = errors.New("catalog already exists with different configuration; DROP then CREATE")

// catalogNameRe matches Trino's identifiers for both catalog and connector
// names: lowercase ASCII, starts with a letter, may contain lowercase
// letters/digits/underscore, up to 63 characters. Identical to the CHECK
// constraint on trino_catalog_registry and the regex used by the Java side
// (CatalogRow / FileCatalogStore).
var catalogNameRe = regexp.MustCompile("^[a-z][a-z0-9_]{0,62}$")

// CreateCatalog registers a catalog in Trino at runtime (requires
// catalog.management=dynamic). The DDL is built with:
//   - Name and Connector validated against catalogNameRe BEFORE interpolation
//     (no sanitization — fail fast).
//   - Property keys double-quoted (required because keys contain hyphens).
//   - Property values single-quoted with ' escaped as ”.
//   - Property keys iterated in sorted alphabetical order, so the generated
//     text is deterministic and the catalog_version hash (bindings/trino.Version)
//     is reproducible.
//   - Secrets in spec.SecretKeys replaced with the
//     @baleia-secret[<Name>:<key>] placeholder in the DDL text ONLY.
//
// This function never logs or returns the real secret values.
func (c *Client) CreateCatalog(ctx context.Context, spec CatalogSpec) error {
	if err := validateCatalogName(spec.Name); err != nil {
		return fmt.Errorf("invalid catalog name: %w", err)
	}
	if err := validateCatalogName(spec.Connector); err != nil {
		return fmt.Errorf("invalid connector name: %w", err)
	}
	ddl := buildCreateCatalogDDL(spec)
	if _, err := c.Execute(ctx, ddl, "", ""); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return ErrCatalogExists
		}
		return err
	}
	return nil
}

// DropCatalog removes a runtime-registered catalog. DROP CATALOG does not
// interrupt running queries that use the catalog but makes it unavailable
// to new queries (Trino docs + CR-014 §8.3 — draining is a server concern).
// "does not exist" is treated as success.
func (c *Client) DropCatalog(ctx context.Context, name string) error {
	if err := validateCatalogName(name); err != nil {
		return fmt.Errorf("invalid catalog name: %w", err)
	}
	ddl := fmt.Sprintf("DROP CATALOG IF EXISTS %s", name)
	if _, err := c.Execute(ctx, ddl, "", ""); err != nil {
		if strings.Contains(err.Error(), "does not exist") ||
			strings.Contains(err.Error(), "not found") {
			return nil
		}
		return err
	}
	return nil
}

// ListCatalogs returns the names of all catalogs currently registered in
// Trino. Used by the reconcile endpoint (POST /trino/reconcile) to
// compare the registry's enabled set against Trino's live set.
func (c *Client) ListCatalogs(ctx context.Context) ([]string, error) {
	res, err := c.Execute(ctx, "SELECT catalog_name FROM system.metadata.catalogs ORDER BY catalog_name", "", "")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Data))
	for _, row := range res.Data {
		if len(row) == 0 {
			continue
		}
		if s, ok := row[0].(string); ok {
			names = append(names, s)
		}
	}
	return names, nil
}

// validateCatalogName enforces the Trino identifier rules BEFORE interpolation.
func validateCatalogName(name string) error {
	if name == "" {
		return errors.New("empty")
	}
	if !catalogNameRe.MatchString(name) {
		return fmt.Errorf("must match %s (got %q)", catalogNameRe, name)
	}
	return nil
}

// buildCreateCatalogDDL renders spec into a CREATE CATALOG statement.
// Secrets in spec.SecretKeys are replaced with @baleia-secret[...] in the
// DDL text only; spec.Properties retains the real value (callers must
// already have written that to the registry via the Trino binder, internal/bindings/trino).
func buildCreateCatalogDDL(spec CatalogSpec) string {
	secretKeySet := make(map[string]struct{}, len(spec.SecretKeys))
	for _, k := range spec.SecretKeys {
		secretKeySet[k] = struct{}{}
	}

	propNames := make([]string, 0, len(spec.Properties))
	for k := range spec.Properties {
		propNames = append(propNames, k)
	}
	sort.Strings(propNames)

	var b strings.Builder
	fmt.Fprintf(&b, "CREATE CATALOG %s USING %s WITH (", spec.Name, spec.Connector)
	for i, k := range propNames {
		if i > 0 {
			b.WriteString(",")
		}
		v := spec.Properties[k]
		if _, redact := secretKeySet[k]; redact {
			v = SecretPlaceholder(spec.Name, k)
		}
		escapedValue := strings.ReplaceAll(v, "'", "''")
		fmt.Fprintf(&b, "\n\t\"%s\" = '%s'", k, escapedValue)
	}
	b.WriteString("\n)")
	return b.String()
}

// ExecuteOptions configures a single statement execution.
type ExecuteOptions struct {
	Catalog string
	Schema  string
	// User overrides the client's configured identity for this statement. The
	// admin client never sets it (it runs catalog DDL); per-user executions
	// set it to the mapped Baleia user identity.
	User string
	// OnQueryID is called once, as soon as Trino assigns a query id. The
	// activity uses it to heartbeat the id before polling begins.
	OnQueryID func(id string)
	// OnProgress is called after every poll response, so the activity can
	// heartbeat and therefore receive cancellation.
	OnProgress func()
	// SessionProperties are sent as X-Trino-Session: k=v, ...
	SessionProperties map[string]string
	// PreparedStatements are sent as X-Trino-Prepared-Statement: name=sql, ...
	PreparedStatements map[string]string
	// ExtraCredentials are sent as X-Trino-Extra-Credential (the catalog
	// guard's read-only mode is baleia.mode=readonly).
	ExtraCredentials map[string]string
	// Correlation: X-Trino-Source, X-Trino-Client-Tags, X-Trino-Client-Info
	// and X-Trino-Trace-Token.
	Source     string
	ClientTags []string
	ClientInfo string
	TraceToken string
	// OnHeaders receives the headers of every response, so a client-side
	// session can apply X-Trino-Set-* and prepared statement changes.
	OnHeaders func(h http.Header)
}

// trinoQueryIDRe matches Trino's query id format, e.g.
// 20240101_120000_00001_abcde. kill_query interpolates the id into SQL, so it
// is validated before use.
var trinoQueryIDRe = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9]{5}_[a-z0-9]{5}$`)

// Execute runs sql with a catalog and schema, buffering the whole result.
// It is a thin wrapper over ExecuteWithOptions retained for the browsing call
// sites that do not need progress callbacks or per-statement identity.
func (c *Client) Execute(ctx context.Context, sql, catalog, schema string) (*QueryResult, error) {
	return c.ExecuteWithOptions(ctx, sql, ExecuteOptions{Catalog: catalog, Schema: schema})
}

// ExecuteWithOptions runs a single statement, invoking the callbacks as the
// statement progresses. When ctx is cancelled mid-poll it abandons the query on
// Trino with a DELETE to the last nextUri before returning ctx.Err().
func (c *Client) ExecuteWithOptions(ctx context.Context, sql string, opts ExecuteOptions) (*QueryResult, error) {
	result := &QueryResult{}

	queryID, err := c.pumpStatement(ctx, sql, opts, func(cols []Column, rows [][]any) error {
		if len(result.Columns) == 0 && len(cols) > 0 {
			result.Columns = cols
		}
		if rows != nil {
			result.Data = append(result.Data, rows...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result.QueryID = queryID
	result.RowCount = int64(len(result.Data))
	return result, nil
}

// ExecuteStream runs a single statement and hands every page of rows to onPage
// as it arrives. Columns are delivered on the first page that carries them.
// The Trino query id is returned once it is assigned.
//
// onPage callback errors abort the polling loop and are returned as-is,
// abandoning the query on Trino (same as context cancellation).
func (c *Client) ExecuteStream(ctx context.Context, sql string, opts ExecuteOptions,
	onPage func(cols []Column, rows [][]any) error) (string, error) {
	return c.pumpStatement(ctx, sql, opts, onPage)
}

// pumpStatement drives the Trino statement protocol, calling onPage for the
// initial response and every subsequent page.
func (c *Client) pumpStatement(ctx context.Context, sql string, opts ExecuteOptions,
	onPage func(cols []Column, rows [][]any) error) (string, error) {
	reqURL := c.baseURL + "/v1/statement"
	user := c.resolveUser(opts)

	body := bytes.NewBufferString(sql)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return "", err
	}
	c.setStatementHeaders(req, opts)

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &TransportError{Err: fmt.Errorf("trino execute: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &TransportError{Status: resp.StatusCode}
	}
	if opts.OnHeaders != nil {
		opts.OnHeaders(resp.Header)
	}

	var sr StatementResponse
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&sr); err != nil {
		return "", fmt.Errorf("trino decode: %w", err)
	}

	if sr.ID != "" && opts.OnQueryID != nil {
		opts.OnQueryID(sr.ID)
	}

	if sr.Error != nil {
		return sr.ID, queryError(sr.ID, sr.Error)
	}

	if err := c.onPage(onPage, sr.Columns, sr.Data); err != nil {
		return sr.ID, err
	}
	if opts.OnProgress != nil {
		opts.OnProgress()
	}

	nextURI := sr.NextURI
	for nextURI != "" {
		if err := ctx.Err(); err != nil {
			c.cancelAbandoned(nextURI, user)
			return "", err
		}

		sr, err = c.next(ctx, nextURI, user, opts.OnHeaders)
		if err != nil {
			if ctx.Err() != nil {
				c.cancelAbandoned(nextURI, user)
				return "", ctx.Err()
			}
			return "", err
		}
		if sr.Error != nil {
			return sr.ID, queryError(sr.ID, sr.Error)
		}
		if err := c.onPage(onPage, sr.Columns, sr.Data); err != nil {
			return "", err
		}
		nextURI = sr.NextURI
		if opts.OnProgress != nil {
			opts.OnProgress()
		}
	}

	return sr.ID, nil
}

// resolveUser returns the per-statement identity when set, else the client's
// configured user.
func (c *Client) resolveUser(opts ExecuteOptions) string {
	if opts.User != "" {
		return opts.User
	}
	return c.user
}

func (c *Client) onPage(onPage func(cols []Column, rows [][]any) error, cols []Column, rows [][]any) error {
	if onPage == nil {
		return nil
	}
	return onPage(cols, rows)
}

// setStatementHeaders applies the identity, catalog/schema and session
// properties to a statement request.
func (c *Client) setStatementHeaders(req *http.Request, opts ExecuteOptions) {
	req.Header.Set("X-Trino-User", c.resolveUser(opts))
	// Without PARAMETRIC_DATETIME Trino rounds timestamps and times to
	// milliseconds for legacy clients; results must keep their precision.
	req.Header.Set("X-Trino-Client-Capabilities", "PARAMETRIC_DATETIME")
	if opts.Catalog != "" {
		req.Header.Set("X-Trino-Catalog", opts.Catalog)
	}
	if opts.Schema != "" {
		req.Header.Set("X-Trino-Schema", opts.Schema)
	}
	if len(opts.SessionProperties) > 0 {
		req.Header.Set("X-Trino-Session", encodeSessionProperties(opts.SessionProperties))
	}
	if len(opts.PreparedStatements) > 0 {
		req.Header.Set("X-Trino-Prepared-Statement", encodeURLPairs(opts.PreparedStatements))
	}
	if len(opts.ExtraCredentials) > 0 {
		req.Header.Set("X-Trino-Extra-Credential", encodeSessionProperties(opts.ExtraCredentials))
	}
	if opts.Source != "" {
		req.Header.Set("X-Trino-Source", opts.Source)
	}
	if len(opts.ClientTags) > 0 {
		req.Header.Set("X-Trino-Client-Tags", strings.Join(opts.ClientTags, ","))
	}
	if opts.ClientInfo != "" {
		req.Header.Set("X-Trino-Client-Info", opts.ClientInfo)
	}
	if opts.TraceToken != "" {
		req.Header.Set("X-Trino-Trace-Token", opts.TraceToken)
	}
	req.Header.Set("Content-Type", "text/plain")
}

// encodeURLPairs renders name=value pairs with URL-encoded values, as Trino
// expects for prepared statements.
func encodeURLPairs(pairs map[string]string) string {
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+url.QueryEscape(pairs[k]))
	}
	return strings.Join(parts, ",")
}

// encodeSessionProperties renders session properties deterministically so the
// generated request is stable across calls.
func encodeSessionProperties(props map[string]string) string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, props[k]))
	}
	return strings.Join(parts, ",")
}

// cancelAbandoned sends DELETE to the last nextUri with a fresh context: the
// caller's context is already cancelled, so the request needs its own.
func (c *Client) cancelAbandoned(nextURI, user string) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Best effort: Trino also abandons a query nobody polls.
	_ = c.deleteURI(reqCtx, nextURI, user)
}

// KillQuery cancels a running query by id. It uses system.runtime.kill_query,
// the only handle available when the caller did not keep the statement's
// nextUri. The query id is validated against Trino's format before it reaches
// the SQL text.
func (c *Client) KillQuery(ctx context.Context, queryID, message string) error {
	if !trinoQueryIDRe.MatchString(queryID) {
		return fmt.Errorf("invalid trino query id %q", queryID)
	}
	if message == "" {
		message = "cancelled by baleia"
	}
	escaped := strings.ReplaceAll(message, "'", "''")
	sql := fmt.Sprintf("CALL system.runtime.kill_query(query_id => '%s', message => '%s')", queryID, escaped)
	_, err := c.Execute(ctx, sql, "", "")
	return err
}

// Cancel abandons a statement via DELETE on its nextUri, as the client's
// configured user.
func (c *Client) Cancel(ctx context.Context, nextURI string) error {
	return c.deleteURI(ctx, nextURI, c.user)
}

func (c *Client) deleteURI(ctx context.Context, nextURI, user string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", nextURI, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Trino-User", user)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return errors.New("trino cancel failed: " + string(b))
	}
	return nil
}

func (c *Client) next(ctx context.Context, uri, user string, onHeaders func(http.Header)) (StatementResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
	if err != nil {
		return StatementResponse{}, err
	}
	req.Header.Set("X-Trino-User", user)

	resp, err := c.http.Do(req)
	if err != nil {
		return StatementResponse{}, &TransportError{Err: fmt.Errorf("trino next: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return StatementResponse{}, &TransportError{Status: resp.StatusCode}
	}
	if onHeaders != nil {
		onHeaders(resp.Header)
	}

	var sr StatementResponse
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&sr); err != nil {
		return StatementResponse{}, fmt.Errorf("trino next decode: %w", err)
	}
	return sr, nil
}

func (c *Client) ListQueries(ctx context.Context) ([]QueryInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/query", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Trino-User", c.user)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trino list queries: %w", err)
	}
	defer resp.Body.Close()

	var queries []QueryInfo
	if err := json.NewDecoder(resp.Body).Decode(&queries); err != nil {
		return nil, fmt.Errorf("trino decode queries: %w", err)
	}
	return queries, nil
}

// ErrQueryGone: the coordinator no longer knows the query (it forgets
// finished queries after query.min-expire-age).
var ErrQueryGone = errors.New("trino: query not known to the coordinator")

// maxQueryInfoBytes bounds the /v1/query/{id} document, which carries the
// whole plan and per-stage statistics.
const maxQueryInfoBytes = 32 << 20

// GetQuery reads one query's info and statistics from /v1/query/{id} (#407).
// An unknown query is ErrQueryGone.
func (c *Client) GetQuery(ctx context.Context, queryID string) (*QueryInfo, error) {
	// The id comes from a request path: escaped, it stays one path segment.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/query/"+url.PathEscape(queryID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Trino-User", c.user)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trino get query: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, ErrQueryGone
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("trino get query: status %d", resp.StatusCode)
	}

	var qi QueryInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxQueryInfoBytes)).Decode(&qi); err != nil {
		return nil, fmt.Errorf("trino decode query: %w", err)
	}
	return &qi, nil
}
