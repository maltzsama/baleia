// Package stored builds the projection of a stored binding: its catalog's
// connection, decrypted, through the engine's binder. The API and the
// worker's reconciler (#37) share it.
package stored

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the slice of the database a projection reads.
type Store interface {
	GetCatalogByID(ctx context.Context, id pgtype.UUID) (db.Catalog, error)
	GetCatalogConnectionByID(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error)
	ListBindingsByEngine(ctx context.Context, engineID pgtype.UUID) ([]db.CatalogBinding, error)
}

// Projector projects stored bindings.
type Projector struct {
	Store     Store
	Keyring   *crypto.Keyring
	Providers *connections.Registry
	Binder    bindings.Binder
}

// Project builds the current projection of b.
func (p Projector) Project(ctx context.Context, b db.CatalogBinding) (bindings.Projection, error) {
	cat, err := p.Store.GetCatalogByID(ctx, b.CatalogID)
	if err != nil {
		return bindings.Projection{}, fmt.Errorf("load catalog: %w", err)
	}
	conn, err := p.Store.GetCatalogConnectionByID(ctx, cat.ConnectionID)
	if err != nil {
		return bindings.Projection{}, fmt.Errorf("load connection: %w", err)
	}
	plain, err := catalogs.OpenCredentials(conn, p.Keyring)
	if err != nil {
		return bindings.Projection{}, err
	}
	cfg, err := providers.FromStored(p.Providers, connections.Kind(conn.Type), conn.ID, conn.Url, conn.Config, plain)
	if err != nil {
		return bindings.Projection{}, err
	}
	extra := map[string]string{}
	if len(b.ExtraProps) > 0 && string(b.ExtraProps) != "null" {
		if err := json.Unmarshal(b.ExtraProps, &extra); err != nil {
			return bindings.Projection{}, fmt.Errorf("%w: extra_props must be a flat string map", bindings.ErrInvalid)
		}
	}
	spec := bindings.BindingSpec{EngineCatalog: b.EngineCatalogName, ExtraProps: extra}
	if b.Connector != nil {
		spec.Connector = *b.Connector
	}
	if cat.SourceRef != nil {
		spec.SourceRef = *cat.SourceRef
	}
	// A Hive-connector catalog of a metastore whose connection also exposes an
	// Iceberg or Delta catalog here delegates those tables to it (#86).
	if err := bindings.HiveRedirection(ctx, p.Store, connections.Kind(conn.Type), conn.ID, b.EngineID, b.CatalogID, spec.Connector, extra); err != nil {
		return bindings.Projection{}, fmt.Errorf("hive redirection: %w", err)
	}
	return p.Binder.Project(spec, cfg)
}

// RunStore persists reconcile runs.
type RunStore interface {
	InsertReconcileRun(ctx context.Context, arg db.InsertReconcileRunParams) (db.InsertReconcileRunRow, error)
}

// SaveRun persists one reconcile run of an engine (#37); the store keeps
// the last 100 per engine.
func SaveRun(ctx context.Context, q RunStore, engineID pgtype.UUID, rep bindings.Report, runErr error) (db.CatalogReconcileRun, error) {
	actions := rep.Actions
	if actions == nil {
		actions = []bindings.Action{}
	}
	raw, err := json.Marshal(actions)
	if err != nil {
		return db.CatalogReconcileRun{}, err
	}
	adopted := rep.Adopted
	if adopted == nil {
		adopted = []string{}
	}
	finished := rep.FinishedAt
	if finished.IsZero() {
		finished = time.Now()
	}
	p := db.InsertReconcileRunParams{
		EngineID: engineID, DryRun: rep.DryRun, Actions: raw, Adopted: adopted,
		StartedAt:  pgtype.Timestamptz{Time: rep.StartedAt, Valid: true},
		FinishedAt: pgtype.Timestamptz{Time: finished, Valid: true},
	}
	if runErr != nil {
		msg := runErr.Error()
		p.Error = &msg
	}
	row, err := q.InsertReconcileRun(ctx, p)
	return db.CatalogReconcileRun(row), err
}
