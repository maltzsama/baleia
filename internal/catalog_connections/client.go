package catalog_connections

import (
	"context"
	"encoding/json"
	"fmt"
)

// ── Tipos compartilhados ──────────────────────────────────────────────────────

type Catalog struct {
	Name string
	Type string
}

type Namespace = []string

type TableIdentifier struct {
	Namespace []string
	Name      string
}

type StructField struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Type     any    `json:"type"`
	Required bool   `json:"required"`
	Doc      string `json:"doc,omitempty"`
}

type Schema struct {
	SchemaID int           `json:"schema-id"`
	Fields   []StructField `json:"fields"`
}

type Snapshot struct {
	SnapshotID       int64             `json:"snapshot-id"`
	ParentSnapshotID *int64            `json:"parent-snapshot-id,omitempty"`
	TimestampMs      int64             `json:"timestamp-ms"`
	ManifestList     string            `json:"manifest-list"`
	Operation        string
	Summary          map[string]string `json:"summary"`
}

type TableMetadata struct {
	TableUUID string     `json:"table-uuid"`
	Location  string     `json:"location"`
	Schemas   []Schema   `json:"schemas"`
	Snapshots []Snapshot `json:"snapshots"`
}

// ── Interface ─────────────────────────────────────────────────────────────────

type Client interface {
	Ping(ctx context.Context) error
	ListCatalogs(ctx context.Context) ([]Catalog, error)
	ListNamespaces(ctx context.Context, catalog string) ([]Namespace, error)
	ListTables(ctx context.Context, catalog, namespace string) ([]TableIdentifier, error)
	LoadTable(ctx context.Context, catalog, namespace, table string) (*TableMetadata, error)
}

// ── Factory ───────────────────────────────────────────────────────────────────

func NewClient(connType, baseURL string, credsJSON *string) (Client, error) {
	var rawCreds []byte
	if credsJSON != nil {
		rawCreds = []byte(*credsJSON)
	}

	switch connType {
	case "polaris":
		return newPolarisClient(baseURL, rawCreds)
	case "nessie":
		return newNessieClient(baseURL, rawCreds)
	case "glue":
		return newGlueClient(baseURL, rawCreds)
	case "hive":
		return newHiveClient(baseURL, rawCreds)
	case "iceberg":
		return newIcebergClient(baseURL, rawCreds)
	default:
		return nil, fmt.Errorf("unknown catalog connection type: %s", connType)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func unmarshalCreds(raw []byte, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}