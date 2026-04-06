package catalog_connections

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type icebergCredentials struct {
	Token          string `json:"token"`
	ClientID       string `json:"client_id"`
	ClientSecret   string `json:"client_secret"`
	OAuth2ServerURI string `json:"oauth2_server_uri"`
}

type icebergClient struct {
	baseURL     string
	creds       icebergCredentials
	httpClient  *http.Client
	token       string
	tokenExpiry time.Time
}

func newIcebergClient(baseURL string, rawCreds []byte) (*icebergClient, error) {
	var creds icebergCredentials
	if err := unmarshalCreds(rawCreds, &creds); err != nil {
		return nil, fmt.Errorf("iceberg credentials: %w", err)
	}
	return &icebergClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		creds:      creds,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *icebergClient) authenticate(ctx context.Context) error {
	// static token has priority
	if c.creds.Token != "" {
		c.token = c.creds.Token
		return nil
	}

	// client credentials flow OAuth2
	if c.creds.ClientID == "" {
		return nil
	}

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return nil
	}

	tokenURL := c.creds.OAuth2ServerURI
	if tokenURL == "" {
		tokenURL = c.baseURL + "/v1/oauth/tokens"
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", c.creds.ClientID)
	data.Set("client_secret", c.creds.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	c.token = result.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn-30) * time.Second)
	return nil
}

func (c *icebergClient) do(ctx context.Context, method, path string) (*http.Response, error) {
	if err := c.authenticate(ctx); err != nil {
		return nil, err
	}
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

func (c *icebergClient) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, "GET", "/v1/config")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("iceberg catalog ping failed (%d)", resp.StatusCode)
	}
	return nil
}

func (c *icebergClient) ListCatalogs(ctx context.Context) ([]Catalog, error) {
	return []Catalog{{Name: "default", Type: "iceberg"}}, nil
}

func (c *icebergClient) ListNamespaces(ctx context.Context, catalog string) ([]Namespace, error) {
	resp, err := c.do(ctx, "GET", fmt.Sprintf("/v1/%s/namespaces", catalog))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("iceberg list namespaces failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Namespaces []Namespace `json:"namespaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Namespaces, nil
}

func (c *icebergClient) ListTables(ctx context.Context, catalog, namespace string) ([]TableIdentifier, error) {
	path := fmt.Sprintf("/v1/%s/namespaces/%s/tables", catalog, namespace)
	resp, err := c.do(ctx, "GET", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("iceberg list tables failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Identifiers []struct {
			Namespace []string `json:"namespace"`
			Name      string   `json:"name"`
		} `json:"identifiers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	tables := make([]TableIdentifier, 0, len(result.Identifiers))
	for _, t := range result.Identifiers {
		tables = append(tables, TableIdentifier{Namespace: t.Namespace, Name: t.Name})
	}
	return tables, nil
}

func (c *icebergClient) LoadTable(ctx context.Context, catalog, namespace, table string) (*TableMetadata, error) {
	path := fmt.Sprintf("/v1/%s/namespaces/%s/tables/%s", catalog, namespace, table)
	resp, err := c.do(ctx, "GET", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("iceberg load table failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Metadata TableMetadata `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	for i, s := range result.Metadata.Snapshots {
		if op, ok := s.Summary["operation"]; ok {
			result.Metadata.Snapshots[i].Operation = op
		}
	}

	return &result.Metadata, nil
}