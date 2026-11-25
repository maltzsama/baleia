package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// APIState is the part of the API dependency graph PrepareAPI needs. It is a
// struct instead of *app.Core because internal/app imports this package.
type APIState struct {
	Store   *gorm.DB
	Keyring *crypto.Keyring
	// TrinoCA is the PEM bundle of BALEIA_TRINO_CA_FILE, empty when unset.
	TrinoCA string
	// Connections validates and discovers incomplete direct connections.
	Connections *connections.Registry
	// DataPolicies publishes the initial data policy revision; nil while data
	// policies are off.
	DataPolicies InitialPublisher
}

// InitialPublisher publishes the data policy revision OPA starts from;
// *datapolicy.Publisher implements it.
type InitialPublisher interface {
	PublishInitial(ctx context.Context) (datapolicy.Revision, bool, error)
}

// PrepareAPI applies the API's deploy-time state in order: the baleia_trino
// role and its password, the default Trino engine seeded from TRINO_URL, the
// one-time split of dual-path connections (ADR 0021 CR-17), then the initial
// data policy revision. It is idempotent and runs before the server starts
// serving.
func PrepareAPI(ctx context.Context, logger *slog.Logger, cfg *config.Config, st APIState) error {
	// Set the baleia_trino password from BALEIA_TRINO_DB_PASSWORD so the role and
	// the Trino plugin's connection stay in sync (no secret in a migration).
	if err := EnsureTrinoRole(ctx, st.Store); err != nil {
		return fmt.Errorf("baleia_trino role: %w", err)
	}
	if err := SetTrinoRolePassword(ctx, st.Store, cfg.TrinoDBPassword); err != nil {
		return fmt.Errorf("baleia_trino password: %w", err)
	}

	// Engine endpoints live in the database; TRINO_URL only seeds and updates
	// the default Trino engine Baleia created itself.
	var engineID uuid.UUID
	if cfg.DefaultEngineID != "" {
		id, err := uuid.Parse(cfg.DefaultEngineID)
		if err != nil {
			return fmt.Errorf("BALEIA_DEFAULT_ENGINE_ID: %w", err)
		}
		engineID = id
	}
	if err := st.Store.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return EnsureDefaultTrinoEngine(ctx, logger, repo.New(tx), DefaultTrinoEngine{
			URL: cfg.TrinoURL, ServiceUser: cfg.TrinoServiceUser, AdminUser: cfg.TrinoAdminUser,
			Password: cfg.TrinoPassword != "", CA: st.TrinoCA, Seal: sealCredentials(st.Keyring),
			EngineID: engineID,
		})
	}); err != nil {
		return fmt.Errorf("default trino engine: %w", err)
	}

	// Split connections that today carry both a direct engine and Trino
	// catalogs into one connection per access path (ADR 0021 CR-17). The store
	// migration backfilled single-path connections; this handles the rest.
	if err := SplitDualPathConnections(ctx, st.Store, st.Keyring, logger); err != nil {
		return fmt.Errorf("split dual-path connections: %w", err)
	}
	if err := ReconcileIncompleteDirectConnections(ctx, st.Store, st.Keyring, st.Connections, logger); err != nil {
		return fmt.Errorf("reconcile incomplete direct connections: %w", err)
	}
	PublishInitialDataPolicies(ctx, logger, st.DataPolicies)
	return nil
}

// PublishInitialDataPolicies publishes the initial data policy revision when
// nothing is published (ADR 0028): until OPA activates a bundle, the engines
// it gates are not ready, and an engine must be ready before the catalog DDL
// the first policy needs. A failure does not stop the API: the worker's
// re-issue pass publishes it too.
func PublishInitialDataPolicies(ctx context.Context, logger *slog.Logger, p InitialPublisher) {
	if p == nil {
		return
	}
	rev, published, err := p.PublishInitial(ctx)
	if err != nil {
		logger.WarnContext(ctx, "publish the initial data policy revision; the worker retries", "err", err)
		return
	}
	if published {
		logger.InfoContext(ctx, "initial data policy revision published", "revision", rev.ID, "number", rev.Number)
	}
}

// sealCredentials seals a connection's credentials with the keyring; nil when
// no keyring is configured (config.Validate refuses a Trino password then).
func sealCredentials(kr *crypto.Keyring) func([]byte, db.UUID) (string, error) {
	if kr == nil {
		return nil
	}
	return func(plain []byte, id db.UUID) (string, error) {
		return kr.Seal(plain, catalogs.CredentialsAAD(id))
	}
}
