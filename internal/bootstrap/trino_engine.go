package bootstrap

import (
	"cmp"
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

// DefaultTrinoEngine is the environment's view of the default Trino engine:
// TRINO_URL, BALEIA_TRINO_SERVICE_USER, BALEIA_TRINO_ADMIN_USER and, for an
// authenticated coordinator (#646), BALEIA_TRINO_PASSWORD and
// BALEIA_TRINO_CA_FILE.
type DefaultTrinoEngine struct {
	URL         string
	ServiceUser string
	AdminUser   string
	// Password reports that BALEIA_TRINO_PASSWORD is set. The connection then
	// logs in as AdminUser with a sealed reference to that variable, never the
	// password itself.
	Password bool
	// CA is the PEM bundle trusted for the coordinator's certificate.
	CA string
	// Seal encrypts the connection's credentials bound to its id (the keyring);
	// required when Password is set.
	Seal func(plain []byte, connID pgtype.UUID) (string, error)
}

// TrinoPasswordRef is the external secret reference the default engine stores
// in place of the password.
const TrinoPasswordRef = "@baleia-secret[env:BALEIA_TRINO_PASSWORD]"

// authKeys are the config keys set only for an authenticated coordinator; they
// are removed when the environment no longer sets them.
var authKeys = []string{"username", "ca_cert"}

func (d DefaultTrinoEngine) config() map[string]string {
	cfg := map[string]string{"url": d.URL}
	if d.ServiceUser != "" {
		cfg["service_user"] = d.ServiceUser
	}
	if d.AdminUser != "" {
		cfg["admin_user"] = d.AdminUser
	}
	if d.Password {
		cfg["username"] = cmp.Or(d.AdminUser, "baleia_admin")
	}
	if d.CA != "" {
		cfg["ca_cert"] = d.CA
	}
	return cfg
}

// credentials seals the password reference for connection id; nil when the
// coordinator takes no password.
func (d DefaultTrinoEngine) credentials(id pgtype.UUID) (*string, error) {
	if !d.Password {
		return nil, nil
	}
	if d.Seal == nil {
		return nil, errors.New("default trino engine: a password needs the keyring")
	}
	raw, err := json.Marshal(map[string]string{"password": TrinoPasswordRef})
	if err != nil {
		return nil, err
	}
	enc, err := d.Seal(raw, id)
	if err != nil {
		return nil, fmt.Errorf("seal default trino credentials: %w", err)
	}
	return &enc, nil
}

// EnsureDefaultTrinoEngine keeps the default Trino cluster's engine in the
// database (docs/adr/0002). Engine endpoints live in the database; the
// environment only seeds and updates the engine Baleia created itself:
//   - no engine on the default cluster: create a system-owned trino connection
//     (created_by NULL) with url = trinoURL and a federated engine on it;
//   - a system-owned connection with another url: update it to trinoURL;
//   - a connection someone owns: leave it, TRINO_URL no longer applies.
//
// Run it in one transaction.
func EnsureDefaultTrinoEngine(ctx context.Context, logger *slog.Logger, q DefaultEngineStore, env DefaultTrinoEngine) error {
	trinoURL := env.URL
	if trinoURL == "" {
		return nil
	}
	row, err := q.GetDefaultTrinoEngine(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return createDefaultTrinoEngine(ctx, logger, q, env)
	case err != nil:
		return fmt.Errorf("default trino engine: %w", err)
	}

	conn := row.CatalogConnection
	if conn.CreatedBy.Valid {
		logger.InfoContext(ctx, "default trino engine is managed by a user; TRINO_URL is not applied", "engine", row.Engine.Name)
		return nil
	}
	var cfg map[string]string
	if len(conn.Config) > 0 {
		if err := json.Unmarshal(conn.Config, &cfg); err != nil {
			return fmt.Errorf("default trino engine config: %w", err)
		}
	}
	want := env.config()
	if cfg == nil {
		cfg = map[string]string{}
	}
	changed := conn.Url != trinoURL
	for k, v := range want {
		if cfg[k] != v {
			cfg[k], changed = v, true
		}
	}
	for _, k := range authKeys {
		if _, ok := want[k]; !ok && cfg[k] != "" {
			delete(cfg, k)
			changed = true
		}
	}
	// The reference is resealed on every start while a password is set, so a
	// password stored on the connection through the API never overrides the
	// environment. A later unset BALEIA_TRINO_PASSWORD leaves it in place: the
	// reference then resolves to nothing and no login is sent.
	var creds *string
	if env.Password {
		if creds, err = env.credentials(conn.ID); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	url := trinoURL
	if _, err := q.UpdateCatalogConnection(ctx, db.UpdateCatalogConnectionParams{ID: conn.ID, Url: &url, Config: raw, CredentialsEnc: creds}); err != nil {
		return fmt.Errorf("update default trino engine url: %w", err)
	}
	logger.InfoContext(ctx, "default trino engine updated from the environment", "engine", row.Engine.Name)
	return nil
}

func createDefaultTrinoEngine(ctx context.Context, logger *slog.Logger, q DefaultEngineStore, env DefaultTrinoEngine) error {
	trinoURL := env.URL
	cluster, err := q.GetDefaultTrinoCluster(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no Trino cluster to back: nothing to bootstrap
	}
	if err != nil {
		return fmt.Errorf("default trino cluster: %w", err)
	}

	raw, err := json.Marshal(env.config())
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
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	creds, err := env.credentials(id)
	if err != nil {
		return err
	}
	conn, err := q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID:             id,
		Name:           name,
		Type:           "trino",
		Url:            trinoURL,
		Config:         raw,
		CredentialsEnc: creds,
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
	logger.InfoContext(ctx, "default trino engine created from TRINO_URL", "engine", eng.Name)
	return nil
}
