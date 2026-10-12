package catalog_connections

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type nessieCredentials struct {
	Token string `json:"token"`
}

type nessieClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func newNessieClient(baseURL string, rawCreds []byte) (*nessieClient, error) {
	var creds nessieCredentials
	if err := unmarshalCreds(rawCreds, &creds); err != nil {
		return nil, fmt.Errorf("nessie credentials: %w", err)
	}
	return &nessieClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      creds.Token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *nessieClient) do(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.httpClient.Do(req)
}

func (c *nessieClient) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, "GET", "/api/v2/config")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("nessie ping failed (%d)", resp.StatusCode)
	}
	return nil
}

func (c *nessieClient) ListCatalogs(ctx context.Context) ([]Catalog, error) {
	// Nessie has no concept of multiple catalogs — it is a single catalog
	return []Catalog{{Name: "nessie", Type: "nessie"}}, nil
}

func (c *nessieClient) ListNamespaces(ctx context.Context, catalog string) ([]Namespace, error) {
	resp, err := c.do(ctx, "GET", "/api/v2/trees/main/entries?filter=entry.type==NAMESPACE")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nessie list namespaces failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Entries []struct {
			Name struct {
				Elements []string `json:"elements"`
			} `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	namespaces := make([]Namespace, 0)
	for _, e := range result.Entries {
		if e.Type == "NAMESPACE" {
			namespaces = append(namespaces, e.Name.Elements)
		}
	}
	return namespaces, nil
}

func (c *nessieClient) ListTables(ctx context.Context, catalog, namespace string) ([]TableIdentifier, error) {
	resp, err := c.do(ctx, "GET", "/api/v2/trees/main/entries?filter=entry.type==ICEBERG_TABLE")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nessie list tables failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Entries []struct {
			Name struct {
				Elements []string `json:"elements"`
			} `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	tables := make([]TableIdentifier, 0)
	for _, e := range result.Entries {
		if e.Type == "ICEBERG_TABLE" && len(e.Name.Elements) > 0 {
			ns := e.Name.Elements[:len(e.Name.Elements)-1]
			name := e.Name.Elements[len(e.Name.Elements)-1]
			// filter by namespace since Nessie doesn't support querying tables by namespace
			if len(ns) > 0 && ns[0] == namespace {
				tables = append(tables, TableIdentifier{Namespace: ns, Name: name})
			}
		}
	}
	return tables, nil
}

func (c *nessieClient) LoadTable(ctx context.Context, catalog, namespace, table string) (*TableMetadata, error) {
	// Nessie doesn't have a specific API to get table metadata, so we return just the location based on the catalog connection URL and table identifier. In a real implementation, you might want to fetch the table metadata from the Nessie API if available.
	return &TableMetadata{
		Location: fmt.Sprintf("%s/%s/%s", c.baseURL, namespace, table),
	}, nil
}