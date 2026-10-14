package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// DefaultEngineStore is the subset of db.Querier EnsureDefaultTrinoEngine uses.
type DefaultEngineStore interface {
	GetDefaultTrinoEngine(ctx context.Context) (db.GetDefaultTrinoEngineRow, error)
	GetDefaultTrinoCluster(ctx context.Context) (db.TrinoCluster, error)
	CatalogConnectionNameExists(ctx context.Context, name string) (bool, error)
	CreateCatalogConnection(ctx context.Context, arg db.CreateCatalogConnectionParams) (db.CatalogConnection, error)
	UpdateCatalogConnection(ctx context.Context, arg db.UpdateCatalogConnectionParams) (db.CatalogConnection, error)
	CreateEngine(ctx context.Context, arg db.CreateEngineParams) (db.Engine, error)
	LinkTrinoClusterEngine(ctx context.Context, arg db.LinkTrinoClusterEngineParams) error
}

// EnsureDefaultTrinoEngine keeps the default Trino cluster's engine in the
// database (docs/adr/0002). Engine endpoints live in the database; TRINO_URL
// only seeds and updates the engine Baleia created itself:
//   - no engine on the default cluster: create a system-owned trino connection
//     (created_by NULL) with url = trinoURL and a federated engine on it;
//   - a system-owned connection with another url: update it to trinoURL;
//   - a connection someone owns: leave it, TRINO_URL no longer applies.
//
// Run it in one transaction.
func EnsureDefaultTrinoEngine(ctx context.Context, q DefaultEngineStore, trinoURL string) error {
	if trinoURL == "" {
		return nil
	}
	row, err := q.GetDefaultTrinoEngine(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return createDefaultTrinoEngine(ctx, q, trinoURL)
	case err != nil:
		return fmt.Errorf("default trino engine: %w", err)
	}

	conn := row.CatalogConnection
	if conn.CreatedBy.Valid {
		slog.Info("default trino engine is managed by a user; TRINO_URL is not applied", "engine", row.Engine.Name)
		return nil
	}
	var cfg map[string]string
	if len(conn.Config) > 0 {
		if err := json.Unmarshal(conn.Config, &cfg); err != nil {
			return fmt.Errorf("default trino engine config: %w", err)
		}
	}
	if cfg["url"] == trinoURL && conn.Url == trinoURL {
		return nil
	}
	if cfg == nil {
		cfg = map[string]string{}
	}
	cfg["url"] = trinoURL
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	url := trinoURL
	if _, err := q.UpdateCatalogConnection(ctx, db.UpdateCatalogConnectionParams{ID: conn.ID, Url: &url, Config: raw}); err != nil {
		return fmt.Errorf("update default trino engine url: %w", err)
	}
	slog.Info("default trino engine url updated from TRINO_URL", "engine", row.Engine.Name)
	return nil
}

func createDefaultTrinoEngine(ctx context.Context, q DefaultEngineStore, trinoURL string) error {
	cluster, err := q.GetDefaultTrinoCluster(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no Trino cluster to back: nothing to bootstrap
	}
	if err != nil {
		return fmt.Errorf("default trino cluster: %w", err)
	}

	raw, err := json.Marshal(map[string]string{"url": trinoURL})
	if err != nil {
		return err
	}
	// A user connection may already use the name; a failed insert would abort
	// the transaction, so look first.
	name := "trino-" + cluster.Name
	for n := 2; ; n++ {
		taken, err := q.CatalogConnectionNameExists(ctx, name)
		if err != nil {
			return fmt.Errorf("default trino connection name: %w", err)
		}
		if !taken {
			break
		}
		name = fmt.Sprintf("trino-%s-%d", cluster.Name, n)
	}
	conn, err := q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID:     pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Name:   name,
		Type:   "trino",
		Url:    trinoURL,
		Config: raw,
	})
	if err != nil {
		return fmt.Errorf("create default trino connection: %w", err)
	}

	eng, err := q.CreateEngine(ctx, db.CreateEngineParams{
		Name:         "default",
		Kind:         "trino",
		Class:        "federated",
		ConnectionID: conn.ID,
		IdentityMode: "impersonation",
	})
	if err != nil {
		return fmt.Errorf("create default trino engine: %w", err)
	}
	if err := q.LinkTrinoClusterEngine(ctx, db.LinkTrinoClusterEngineParams{ID: cluster.ID, EngineID: eng.ID}); err != nil {
		return fmt.Errorf("link default trino cluster: %w", err)
	}
	slog.Info("default trino engine created from TRINO_URL", "engine", eng.Name)
	return nil
}
