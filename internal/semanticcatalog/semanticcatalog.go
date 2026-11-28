// Package semanticcatalog is the contract of the workspace semantic catalog
// that external catalog connectors read through the BI gateway (ADR 0030): the
// semantic models and metrics a BI key's owner can see, with their physical
// source tables named as the key's engine names them.
//
// The package only declares the contract. internal/api builds a Snapshot from
// the semantic layer and the lineage extraction; internal/bigateway serves it.
package semanticcatalog

import (
	"context"
	"errors"
	"time"

	"github.com/maltzsama/baleia/internal/db"
)

// ContractVersion is the version of the JSON contract. It changes only on an
// incompatible change; adding a field is compatible.
const ContractVersion = 1

// ErrTooLarge means the workspace has more items than one snapshot may hold.
var ErrTooLarge = errors.New("semantic catalog exceeds the snapshot limit")

// MaxItems bounds the models plus metrics of one snapshot.
const MaxItems = 10000

// Request identifies whose catalog is read: the BI key's workspace, owner and
// engine. The engine decides how physical tables are named.
type Request struct {
	WorkspaceID db.UUID
	UserID      db.UUID
	EngineID    db.UUID
}

// Reader builds a workspace's semantic catalog for one request.
type Reader interface {
	Read(ctx context.Context, req Request) (Snapshot, error)
}

// Snapshot is the semantic catalog at one instant, sorted by slug.
type Snapshot struct {
	Workspace Workspace
	Models    []Model
	Metrics   []Metric
}

// Workspace identifies the workspace the snapshot belongs to.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Model is a semantic model.
type Model struct {
	ID          string       `json:"id"`
	Slug        string       `json:"slug"`
	Label       string       `json:"label"`
	Description string       `json:"description,omitempty"`
	URL         string       `json:"url,omitempty"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Source      ModelSource  `json:"source"`
	Columns     []Column     `json:"columns"`
	Metrics     []ModelField `json:"metrics"`
	// SourceTables are the physical tables the model reads, as
	// "catalog.schema.table" in the key engine's names. Only tables granted to
	// the workspace and reachable on that engine are listed.
	SourceTables []string `json:"source_tables"`
}

// ModelSource is where a model reads from: a table or a SQL query.
type ModelSource struct {
	Kind string `json:"kind"`
	// Table is the source table in the key engine's names; empty when the
	// model's catalog is not reachable on that engine.
	Table string `json:"table,omitempty"`
	SQL   string `json:"sql,omitempty"`
}

// Column is a visible dimension of a model.
type Column struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Type is Baleia's logical type (string, number, boolean, date,
	// timestamp...).
	Type string `json:"type"`
	// Expression is the column's SQL when it is not a plain column.
	Expression string `json:"expression,omitempty"`
}

// ModelField is a metric defined inside a model.
type ModelField struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// SQL is the statement Baleia compiles for this metric alone, in the key
	// engine's dialect; empty when it cannot be compiled.
	SQL string `json:"sql,omitempty"`
}

// Metric is a standalone metric.
type Metric struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Label       string    `json:"label"`
	Description string    `json:"description,omitempty"`
	URL         string    `json:"url,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	// SQL is the metric's statement in the key engine's dialect; empty when it
	// cannot be rendered.
	SQL          string   `json:"sql,omitempty"`
	SourceTables []string `json:"source_tables"`
}
