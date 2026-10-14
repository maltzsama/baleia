// Package icebergrest is the Iceberg REST catalog client shared by the
// providers that serve that API (Polaris, Nessie, generic REST). It is not a
// provider itself.
package icebergrest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
)

// Auth selects how requests authenticate. Token wins over client credentials;
// with neither, requests are anonymous.
type Auth struct {
	Token        string
	ClientID     string
	ClientSecret string
	// TokenURI is the OAuth2 token endpoint; default <base>/v1/oauth/tokens.
	TokenURI string
	Scope    string
}

// Client talks to one Iceberg REST endpoint.
type Client struct {
	base    string
	auth    Auth
	headers map[string]string
	http    *http.Client

	mu       sync.Mutex
	token    string
	expiry   time.Time
	prefixes map[string]string
}

// New returns a client for the REST base URI (the one /v1/config is under).
// Extra headers are sent on every request (e.g. Polaris-Realm).
func New(base string, auth Auth, headers map[string]string, httpClient *http.Client) *Client {
	return &Client{
		base:     strings.TrimRight(base, "/"),
		auth:     auth,
		headers:  headers,
		http:     httpClient,
		prefixes: map[string]string{},
	}
}

// StatusError is a non-2xx answer. It carries the status only: response
// bodies can echo identifiers and are not forwarded (docs/adr/0008).
type StatusError struct {
	Op     string
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("iceberg rest %s failed (%d)", e.Op, e.Status)
}

func (c *Client) bearer(ctx context.Context) (string, error) {
	if c.auth.Token != "" {
		return c.auth.Token, nil
	}
	if c.auth.ClientID == "" {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiry) {
		return c.token, nil
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.auth.ClientID)
	form.Set("client_secret", c.auth.ClientSecret)
	if c.auth.Scope != "" {
		form.Set("scope", c.auth.Scope)
	}
	tokenURI := c.auth.TokenURI
	if tokenURI == "" {
		tokenURI = c.base + "/v1/oauth/tokens"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("iceberg rest auth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Op: "auth", Status: resp.StatusCode}
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("iceberg rest auth: decode: %w", err)
	}
	ttl := time.Duration(out.ExpiresIn)*time.Second - 30*time.Second
	if ttl <= 0 {
		ttl = time.Minute
	}
	c.token, c.expiry = out.AccessToken, time.Now().Add(ttl)
	return c.token, nil
}

// Get sends an authenticated GET to base+path and decodes the JSON answer.
func (c *Client) Get(ctx context.Context, op, path string, query url.Values, out any) error {
	tok, err := c.bearer(ctx)
	if err != nil {
		return err
	}
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", connections.ErrNotFound, op)
	}
	if resp.StatusCode >= 300 {
		return &StatusError{Op: op, Status: resp.StatusCode}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("iceberg rest %s: decode: %w", op, err)
	}
	return nil
}

// Config calls /v1/config for a warehouse ("" = server default) and returns
// the effective properties: overrides win over defaults. It is the
// reachability and credential check of every Iceberg REST provider.
func (c *Client) Config(ctx context.Context, warehouse string) (map[string]string, error) {
	q := url.Values{}
	if warehouse != "" {
		q.Set("warehouse", warehouse)
	}
	var out struct {
		Defaults  map[string]string `json:"defaults"`
		Overrides map[string]string `json:"overrides"`
	}
	if err := c.Get(ctx, "config", "/v1/config", q, &out); err != nil {
		return nil, err
	}
	props := make(map[string]string, len(out.Defaults)+len(out.Overrides))
	for k, v := range out.Defaults {
		props[k] = v
	}
	for k, v := range out.Overrides {
		props[k] = v
	}
	return props, nil
}

// prefix returns the path prefix the server assigns to a warehouse.
func (c *Client) prefix(ctx context.Context, warehouse string) (string, error) {
	c.mu.Lock()
	p, ok := c.prefixes[warehouse]
	c.mu.Unlock()
	if ok {
		return p, nil
	}
	props, err := c.Config(ctx, warehouse)
	if err != nil {
		return "", err
	}
	p = props["prefix"]
	c.mu.Lock()
	c.prefixes[warehouse] = p
	c.mu.Unlock()
	return p, nil
}

func (c *Client) root(ctx context.Context, warehouse string) (string, error) {
	p, err := c.prefix(ctx, warehouse)
	if err != nil {
		return "", err
	}
	if p == "" {
		return "/v1", nil
	}
	// The server sends the prefix ready for the path (Nessie: "main%7Cwarehouse");
	// escaping it again would double-encode it.
	return "/v1/" + p, nil
}

// encodeNamespace joins levels with the unit separator, as the spec requires.
func encodeNamespace(ns connections.Namespace) string {
	return url.PathEscape(strings.Join(ns, "\x1f"))
}

// Reader returns a MetadataReader over a fixed catalog list. warehouseOf maps
// a catalog name to the warehouse sent to the server.
func (c *Client) Reader(catalogs []connections.CatalogRef, warehouseOf func(catalog string) string) connections.MetadataReader {
	return &reader{c: c, catalogs: catalogs, warehouseOf: warehouseOf}
}

type reader struct {
	c           *Client
	catalogs    []connections.CatalogRef
	warehouseOf func(string) string
	// ListCatalogs overrides the static list when set (Polaris lists them
	// through its management API).
	listCatalogs func(context.Context) ([]connections.CatalogRef, error)
}

// ReaderWithCatalogs is Reader with a dynamic catalog list.
func (c *Client) ReaderWithCatalogs(list func(context.Context) ([]connections.CatalogRef, error), warehouseOf func(string) string) connections.MetadataReader {
	return &reader{c: c, listCatalogs: list, warehouseOf: warehouseOf}
}

func (r *reader) ListCatalogs(ctx context.Context) ([]connections.CatalogRef, error) {
	if r.listCatalogs != nil {
		return r.listCatalogs(ctx)
	}
	return r.catalogs, nil
}

func (r *reader) ListNamespaces(ctx context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	root, err := r.c.root(ctx, r.warehouseOf(catalog))
	if err != nil {
		return nil, err
	}
	var all []connections.Namespace
	token := ""
	for {
		q := url.Values{}
		if len(parent) > 0 {
			q.Set("parent", strings.Join(parent, "\x1f"))
		}
		if token != "" {
			q.Set("pageToken", token)
		}
		var out struct {
			Namespaces    [][]string `json:"namespaces"`
			NextPageToken string     `json:"next-page-token"`
		}
		if err := r.c.Get(ctx, "list namespaces", root+"/namespaces", q, &out); err != nil {
			return nil, err
		}
		for _, ns := range out.Namespaces {
			all = append(all, ns)
		}
		if out.NextPageToken == "" || out.NextPageToken == token {
			return all, nil
		}
		token = out.NextPageToken
	}
}

func (r *reader) ListTables(ctx context.Context, catalog string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	root, err := r.c.root(ctx, r.warehouseOf(catalog))
	if err != nil {
		return nil, "", err
	}
	q := url.Values{}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	var out struct {
		Identifiers []struct {
			Namespace []string `json:"namespace"`
			Name      string   `json:"name"`
		} `json:"identifiers"`
		NextPageToken string `json:"next-page-token"`
	}
	if err := r.c.Get(ctx, "list tables", root+"/namespaces/"+encodeNamespace(ns)+"/tables", q, &out); err != nil {
		return nil, "", err
	}
	tables := make([]connections.TableRef, 0, len(out.Identifiers))
	for _, id := range out.Identifiers {
		tables = append(tables, connections.TableRef{Catalog: catalog, Namespace: id.Namespace, Name: id.Name, Type: "TABLE"})
	}
	return tables, out.NextPageToken, nil
}

type tableMetadata struct {
	FormatVersion   int               `json:"format-version"`
	TableUUID       string            `json:"table-uuid"`
	Location        string            `json:"location"`
	CurrentSchemaID int               `json:"current-schema-id"`
	CurrentSnapshot *int64            `json:"current-snapshot-id"`
	Properties      map[string]string `json:"properties"`
	Schemas         []struct {
		SchemaID int `json:"schema-id"`
		Fields   []struct {
			ID       int             `json:"id"`
			Name     string          `json:"name"`
			Type     json.RawMessage `json:"type"`
			Required bool            `json:"required"`
			Doc      string          `json:"doc"`
		} `json:"fields"`
	} `json:"schemas"`
	Snapshots []struct {
		SnapshotID       int64             `json:"snapshot-id"`
		ParentSnapshotID *int64            `json:"parent-snapshot-id"`
		TimestampMs      int64             `json:"timestamp-ms"`
		Summary          map[string]string `json:"summary"`
	} `json:"snapshots"`
}

func (r *reader) LoadTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	root, err := r.c.root(ctx, r.warehouseOf(t.Catalog))
	if err != nil {
		return nil, err
	}
	var out struct {
		Metadata tableMetadata `json:"metadata"`
	}
	path := root + "/namespaces/" + encodeNamespace(t.Namespace) + "/tables/" + url.PathEscape(t.Name)
	if err := r.c.Get(ctx, "load table", path, nil, &out); err != nil {
		return nil, err
	}
	return toTableMeta(t, out.Metadata), nil
}

func toTableMeta(t connections.TableRef, m tableMetadata) *connections.TableMeta {
	meta := &connections.TableMeta{
		Ref:        t,
		Format:     "iceberg",
		Properties: map[string]string{"location": m.Location, "table_uuid": m.TableUUID, "format_version": fmt.Sprint(m.FormatVersion)},
	}
	if meta.Ref.Type == "" {
		meta.Ref.Type = "TABLE"
	}
	if m.CurrentSnapshot != nil {
		meta.Properties["current_snapshot_id"] = fmt.Sprint(*m.CurrentSnapshot)
	}
	meta.Comment = m.Properties["comment"]
	for _, s := range m.Schemas {
		if s.SchemaID != m.CurrentSchemaID {
			continue
		}
		for i, f := range s.Fields {
			meta.Columns = append(meta.Columns, connections.Column{
				Name:       f.Name,
				NativeType: typeName(f.Type),
				Nullable:   !f.Required,
				Comment:    f.Doc,
				Position:   i + 1,
			})
		}
	}
	for _, s := range m.Snapshots {
		meta.Snapshots = append(meta.Snapshots, connections.Snapshot{
			ID:          s.SnapshotID,
			ParentID:    s.ParentSnapshotID,
			TimestampMs: s.TimestampMs,
			Operation:   s.Summary["operation"],
			Summary:     s.Summary,
		})
	}
	return meta
}

// typeName renders an Iceberg type: primitives are strings, nested types are
// objects whose "type" is struct, list or map.
func typeName(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Type != "" {
		return obj.Type
	}
	return string(raw)
}

func (r *reader) Close() error { return nil }
