package catalog_connections

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ── Glue ──────────────────────────────────────────────────────────────────────

type glueCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region"`
}

type glueClient struct {
	baseURL    string
	creds      glueCredentials
	httpClient *http.Client
}

func newGlueClient(baseURL string, rawCreds []byte) (*glueClient, error) {
	var creds glueCredentials
	if err := unmarshalCreds(rawCreds, &creds); err != nil {
		return nil, fmt.Errorf("glue credentials: %w", err)
	}
	return &glueClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		creds:      creds,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *glueClient) Ping(ctx context.Context) error {
	return fmt.Errorf("glue not yet implemented")
}

func (c *glueClient) ListCatalogs(ctx context.Context) ([]Catalog, error) {
	return nil, fmt.Errorf("glue not yet implemented")
}

func (c *glueClient) ListNamespaces(ctx context.Context, catalog string) ([]Namespace, error) {
	return nil, fmt.Errorf("glue not yet implemented")
}

func (c *glueClient) ListTables(ctx context.Context, catalog, namespace string) ([]TableIdentifier, error) {
	return nil, fmt.Errorf("glue not yet implemented")
}

func (c *glueClient) LoadTable(ctx context.Context, catalog, namespace, table string) (*TableMetadata, error) {
	return nil, fmt.Errorf("glue not yet implemented")
}

// ── Hive ──────────────────────────────────────────────────────────────────────

type hiveCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type hiveClient struct {
	baseURL    string
	creds      hiveCredentials
	httpClient *http.Client
}

func newHiveClient(baseURL string, rawCreds []byte) (*hiveClient, error) {
	var creds hiveCredentials
	if err := unmarshalCreds(rawCreds, &creds); err != nil {
		return nil, fmt.Errorf("hive credentials: %w", err)
	}
	return &hiveClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		creds:      creds,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *hiveClient) Ping(ctx context.Context) error {
	return fmt.Errorf("hive not yet implemented")
}

func (c *hiveClient) ListCatalogs(ctx context.Context) ([]Catalog, error) {
	return nil, fmt.Errorf("hive not yet implemented")
}

func (c *hiveClient) ListNamespaces(ctx context.Context, catalog string) ([]Namespace, error) {
	return nil, fmt.Errorf("hive not yet implemented")
}

func (c *hiveClient) ListTables(ctx context.Context, catalog, namespace string) ([]TableIdentifier, error) {
	return nil, fmt.Errorf("hive not yet implemented")
}

func (c *hiveClient) LoadTable(ctx context.Context, catalog, namespace, table string) (*TableMetadata, error) {
	return nil, fmt.Errorf("hive not yet implemented")
}

// silence unused import errors for now, since these clients are not fully implemented yet
var (
	_ = json.Marshal
	_ = http.DefaultClient
)