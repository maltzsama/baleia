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

type polarisCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type polarisClient struct {
	baseURL     string
	creds       polarisCredentials
	httpClient  *http.Client
	token       string
	tokenExpiry time.Time
}

func newPolarisClient(baseURL string, rawCreds []byte) (*polarisClient, error) {
	var creds polarisCredentials
	if err := unmarshalCreds(rawCreds, &creds); err != nil {
		return nil, fmt.Errorf("polaris credentials: %w", err)
	}
	if creds.ClientID == "" || creds.ClientSecret == "" {
		return nil, fmt.Errorf("polaris requires client_id and client_secret")
	}
	return &polarisClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		creds:      creds,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *polarisClient) authenticate(ctx context.Context) error {
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return nil
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", c.creds.ClientID)
	data.Set("client_secret", c.creds.ClientSecret)
	data.Set("scope", "PRINCIPAL_ROLE:ALL")

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/api/catalog/v1/oauth/tokens",
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return fmt.Errorf("polaris auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("polaris auth: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("polaris auth failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("polaris auth decode: %w", err)
	}

	c.token = result.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn-30) * time.Second)
	return nil
}

func (c *polarisClient) do(ctx context.Context, method, path string) (*http.Response, error) {
	if err := c.authenticate(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Polaris-Realm", "POLARIS")
	return c.httpClient.Do(req)
}

func (c *polarisClient) Ping(ctx context.Context) error {
	if err := c.authenticate(ctx); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/management/v1/catalogs", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Polaris-Realm", "POLARIS")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("polaris ping failed (%d)", resp.StatusCode)
	}
	return nil
}

func (c *polarisClient) ListCatalogs(ctx context.Context) ([]Catalog, error) {
	resp, err := c.do(ctx, "GET", "/api/management/v1/catalogs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("polaris list catalogs failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Catalogs []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"catalogs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	catalogs := make([]Catalog, 0, len(result.Catalogs))
	for _, c := range result.Catalogs {
		catalogs = append(catalogs, Catalog{Name: c.Name, Type: c.Type})
	}
	return catalogs, nil
}

func (c *polarisClient) ListNamespaces(ctx context.Context, catalog string) ([]Namespace, error) {
	resp, err := c.do(ctx, "GET", fmt.Sprintf("/api/catalog/v1/%s/namespaces", catalog))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("polaris list namespaces failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Namespaces []Namespace `json:"namespaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Namespaces, nil
}

func (c *polarisClient) ListTables(ctx context.Context, catalog, namespace string) ([]TableIdentifier, error) {
	path := fmt.Sprintf("/api/catalog/v1/%s/namespaces/%s/tables", catalog, namespace)
	resp, err := c.do(ctx, "GET", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("polaris list tables failed (%d): %s", resp.StatusCode, string(body))
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

func (c *polarisClient) LoadTable(ctx context.Context, catalog, namespace, table string) (*TableMetadata, error) {
	path := fmt.Sprintf("/api/catalog/v1/%s/namespaces/%s/tables/%s", catalog, namespace, table)
	resp, err := c.do(ctx, "GET", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("polaris load table failed (%d): %s", resp.StatusCode, string(body))
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