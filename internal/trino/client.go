package trino

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL string
	user    string
	http    *http.Client
}

func New(baseURL, user string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		http:    &http.Client{},
	}
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type StatementStats struct {
	State      string `json:"state"`
	Rows       int64  `json:"processedRows"`
	Bytes      int64  `json:"processedBytes"`
	ElapsedMs  int64  `json:"elapsedTimeMillis"`
}

type StatementResponse struct {
	ID      string           `json:"id"`
	InfoURI string           `json:"infoUri"`
	NextURI string           `json:"nextUri"`
	Stats   StatementStats   `json:"stats"`
	Columns []Column         `json:"columns"`
	Data    [][]interface{}  `json:"data"`
	Error   *StatementError  `json:"error"`
}

type StatementError struct {
	Message    string      `json:"message"`
	ErrorCode  interface{} `json:"errorCode"`
	ErrorName  string      `json:"errorName"`
	ErrorType  string      `json:"errorType"`
}

type QueryResult struct {
	QueryID  string
	Columns  []Column
	Data     [][]interface{}
	RowCount int64
}

// CatalogParams defines the configuration for registering any catalog in Trino.
type CatalogParams struct {
	Name         string
	Connector    string // iceberg, postgresql, mysql, etc.
	Properties   map[string]string
}

// IcebergCatalogParams configures a dynamic Iceberg REST catalog registration.
type IcebergCatalogParams struct {
	Name       string
	RestURI    string
	Warehouse  string
	Credential string
	Scope      string
}

// CreateCatalog registers a catalog in Trino at runtime (requires
// catalog.management=dynamic). Idempotent: "already exists" errors are ignored.
func (c *Client) CreateCatalog(ctx context.Context, p CatalogParams) error {
	props := make([]string, 0, len(p.Properties))
	for k, v := range p.Properties {
		props = append(props, fmt.Sprintf(`"%s" = '%s'`, k, v))
	}
	ddl := fmt.Sprintf("CREATE CATALOG %s USING %s WITH (\n\t%s\n)",
		p.Name, p.Connector, strings.Join(props, ",\n\t"))

	if _, err := c.Execute(ctx, ddl, "", ""); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// CreateIcebergCatalog is a convenience wrapper for Iceberg REST catalogs.
func (c *Client) CreateIcebergCatalog(ctx context.Context, p IcebergCatalogParams) error {
	scope := p.Scope
	if scope == "" {
		scope = "PRINCIPAL_ROLE:ALL"
	}

	ddl := fmt.Sprintf(`CREATE CATALOG %s USING iceberg WITH (
	"iceberg.catalog.type" = 'rest',
	"iceberg.rest-catalog.uri" = '%s',
	"iceberg.rest-catalog.warehouse" = '%s',
	"iceberg.rest-catalog.security" = 'OAUTH2',
	"iceberg.rest-catalog.oauth2.credential" = '%s',
	"iceberg.rest-catalog.oauth2.scope" = '%s',
	"iceberg.rest-catalog.vended-credentials-enabled" = 'true',
	"fs.native-s3.enabled" = 'true'
)`, p.Name, p.RestURI, p.Warehouse, p.Credential, scope)

	if _, err := c.Execute(ctx, ddl, "", ""); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) Execute(ctx context.Context, sql, catalog, schema string) (*QueryResult, error) {
	reqURL := c.baseURL + "/v1/statement"

	body := bytes.NewBufferString(sql)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Trino-User", c.user)
	if catalog != "" {
		req.Header.Set("X-Trino-Catalog", catalog)
	}
	if schema != "" {
		req.Header.Set("X-Trino-Schema", schema)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trino execute: %w", err)
	}
	defer resp.Body.Close()

	var sr StatementResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("trino decode: %w", err)
	}

	if sr.Error != nil {
		return nil, fmt.Errorf("trino error: %s (%s)", sr.Error.Message, sr.Error.ErrorName)
	}

	result := &QueryResult{
		QueryID: sr.ID,
		Columns: sr.Columns,
	}

	if sr.Data != nil {
		result.Data = append(result.Data, sr.Data...)
	}

	for sr.NextURI != "" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		sr, err = c.next(ctx, sr.NextURI)
		if err != nil {
			return nil, err
		}
		if sr.Error != nil {
			return nil, fmt.Errorf("trino error: %s (%s)", sr.Error.Message, sr.Error.ErrorName)
		}
		if sr.Data != nil {
			result.Data = append(result.Data, sr.Data...)
		}
		if sr.Columns != nil && len(result.Columns) == 0 {
			result.Columns = sr.Columns
		}
	}

	result.RowCount = int64(len(result.Data))
	return result, nil
}

func (c *Client) Cancel(ctx context.Context, nextURI string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", nextURI, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Trino-User", c.user)

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

func (c *Client) next(ctx context.Context, uri string) (StatementResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
	if err != nil {
		return StatementResponse{}, err
	}
	req.Header.Set("X-Trino-User", c.user)

	resp, err := c.http.Do(req)
	if err != nil {
		return StatementResponse{}, fmt.Errorf("trino next: %w", err)
	}
	defer resp.Body.Close()

	var sr StatementResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return StatementResponse{}, fmt.Errorf("trino next decode: %w", err)
	}
	return sr, nil
}

type QueryInfo struct {
	QueryID              string `json:"queryId"`
	Session              string `json:"session"`
	State                string `json:"state"`
	Query                string `json:"query"`
	User                 string `json:"user"`
	Catalog              string `json:"catalog"`
	Schema               string `json:"schema"`
	ResourceGroupID      []string `json:"resourceGroupId"`
	QueryType            string   `json:"queryType"`
	Created              string   `json:"createTime"`
	Ended                string   `json:"endTime"`
	ElapsedMs            *int     `json:"elapsedTimeMillis"`
	CPUMs                *int     `json:"cpuTimeMillis"`
	PhysicalInputBytes   *int64   `json:"physicalInputBytes"`
	OutputRows           *int64   `json:"outputRows"`
	ErrorCode            string   `json:"errorCode"`
	ErrorType            string   `json:"errorType"`
	FailureMessage       string   `json:"failureMessage"`
}

type ClusterStats struct {
	RunningQueries  int `json:"runningQueries"`
	QueuedQueries   int `json:"queuedQueries"`
	BlockedQueries  int `json:"blockedQueries"`
	ActiveWorkers   int `json:"activeWorkers"`
	TotalMemoryBytes    *int64 `json:"totalMemoryBytes"`
	FreeMemoryBytes     *int64 `json:"freeMemoryBytes"`
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

func (c *Client) GetQuery(ctx context.Context, queryID string) (*QueryInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/query/"+queryID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Trino-User", c.user)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trino get query: %w", err)
	}
	defer resp.Body.Close()

	var qi QueryInfo
	if err := json.NewDecoder(resp.Body).Decode(&qi); err != nil {
		return nil, fmt.Errorf("trino decode query: %w", err)
	}
	return &qi, nil
}

func (c *Client) GetClusterStats(ctx context.Context) (*ClusterStats, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/cluster", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Trino-User", c.user)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trino get cluster: %w", err)
	}
	defer resp.Body.Close()

	var raw interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("trino decode cluster: %w", err)
	}

	cs := &ClusterStats{}
	if m, ok := raw.(map[string]interface{}); ok {
		if v, ok := m["runningQueries"].(float64); ok {
			cs.RunningQueries = int(v)
		}
		if v, ok := m["queuedQueries"].(float64); ok {
			cs.QueuedQueries = int(v)
		}
		if v, ok := m["blockedQueries"].(float64); ok {
			cs.BlockedQueries = int(v)
		}
		if v, ok := m["activeWorkers"].(float64); ok {
			cs.ActiveWorkers = int(v)
		}
	}

	return cs, nil
}
