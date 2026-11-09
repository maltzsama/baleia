package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"gorm.io/gorm"
)

// APIState is the part of the API dependency graph PrepareAPI needs. It is a
// struct instead of *app.Core because internal/app imports this package.
type APIState struct {
	Store   *gorm.DB
	Keyring *crypto.Keyring
	// TrinoCA is the PEM bundle of BALEIA_TRINO_CA_FILE, empty when unset.
	TrinoCA string
}

// PrepareAPI applies the API's deploy-time state in order: the baleia_trino
// role and its password, the default Trino engine seeded from TRINO_URL, then
// the one-time split of dual-path connections (ADR 0021 CR-17). It is
// idempotent and runs before the server starts serving.
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
	if err := st.Store.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return EnsureDefaultTrinoEngine(ctx, logger, repo.New(tx), DefaultTrinoEngine{
			URL: cfg.TrinoURL, ServiceUser: cfg.TrinoServiceUser, AdminUser: cfg.TrinoAdminUser,
			Password: cfg.TrinoPassword != "", CA: st.TrinoCA, Seal: sealCredentials(st.Keyring),
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
	return nil
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
