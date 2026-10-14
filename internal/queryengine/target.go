// Package queryengine is the contract between Baleia and the engines that run
// SQL (docs/adr/0002, 0006). This file holds the execution target; the engine
// contract itself lands with #20.
package queryengine

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
)

// Target is where an execution runs: a binding (catalog × engine) plus an
// optional schema. It is resolved and authorized per request (#13) and stored
// with the execution.
type Target struct {
	EngineID      pgtype.UUID
	EngineKind    string // "trino", "postgresql", …
	EngineName    string
	BindingID     pgtype.UUID
	CatalogID     pgtype.UUID
	CatalogName   string // logical name shown in the UI
	EngineCatalog string // the catalog's name in SQL on that engine
	Schema        string // optional; multi-level namespaces are joined by the dialect (#14)
	// ReadOnly comes from the workspace's permission on the catalog: a read
	// grant runs in a read-only session where the engine can enforce it
	// (docs/adr/0011, #20).
	ReadOnly bool
}

// snapshot is the immutable record of a target stored with an execution. It
// carries names only, never connection details or secrets.
type snapshot struct {
	EngineKind    string  `json:"engine_kind"`
	EngineName    string  `json:"engine_name"`
	CatalogName   string  `json:"catalog_name"`
	EngineCatalog string  `json:"engine_catalog"`
	Schema        *string `json:"schema"`
	ReadOnly      bool    `json:"read_only"`
}

// Snapshot is the JSON stored in query_executions.target_snapshot.
func (t Target) Snapshot() ([]byte, error) {
	s := snapshot{
		EngineKind:    t.EngineKind,
		EngineName:    t.EngineName,
		CatalogName:   t.CatalogName,
		EngineCatalog: t.EngineCatalog,
		ReadOnly:      t.ReadOnly,
	}
	if t.Schema != "" {
		s.Schema = &t.Schema
	}
	return json.Marshal(s)
}
