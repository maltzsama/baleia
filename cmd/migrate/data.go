package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/migrate"
)

// migrateCredentials re-seals legacy connection credentials (encrypted under
// JWT_SECRET) as v1 envelopes under BALEIA_ENCRYPTION_KEY, in one transaction.
func migrateCredentials(ctx context.Context, logger *slog.Logger, cfg *config.Config) error {
	if cfg.EncryptionKey == "" {
		return fmt.Errorf("BALEIA_ENCRYPTION_KEY must be set")
	}
	// Legacy values are opened with JWT_SECRET; a missing or default one would
	// silently fail every row.
	if err := config.ValidateSecret(cfg.JWTSecret); err != nil {
		return err
	}
	kr, err := crypto.NewKeyring(cfg.EncryptionKey, cfg.EncryptionKeyPrevious)
	if err != nil {
		return err
	}
	db, closeDB, err := openSQLDB(ctx, logger, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeDB() }()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	res, err := migrate.MigrateCredentials(ctx, migrate.NewSQLCredentialStore(tx), kr, cfg.JWTSecret)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	logger.InfoContext(ctx, "credentials migrated", "migrated", res.Migrated, "resealed", res.Resealed, "already_v1", res.Skipped, "failed", len(res.Failed))
	for _, id := range res.Failed {
		logger.WarnContext(ctx, "credentials left untouched: could not be decrypted with JWT_SECRET, re-enter them", "connection_id", id)
	}
	return nil
}

// migrateOIDCProviders seals legacy plaintext auth_providers.config_enc values
// and re-seals rows under the current key, in one transaction (#278).
func migrateOIDCProviders(ctx context.Context, logger *slog.Logger, cfg *config.Config) error {
	if cfg.EncryptionKey == "" {
		return fmt.Errorf("BALEIA_ENCRYPTION_KEY must be set")
	}
	kr, err := crypto.NewKeyring(cfg.EncryptionKey, cfg.EncryptionKeyPrevious)
	if err != nil {
		return err
	}
	db, closeDB, err := openSQLDB(ctx, logger, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeDB() }()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	res, err := migrate.MigrateOIDCProviders(ctx, migrate.NewSQLProviderStore(tx), kr)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	logger.InfoContext(ctx, "oidc providers migrated", "migrated", res.Migrated, "resealed", res.Resealed, "already_v1", res.Skipped, "failed", len(res.Failed))
	for _, id := range res.Failed {
		logger.WarnContext(ctx, "oidc provider left untouched: not valid plaintext or a known key envelope", "provider_id", id)
	}
	return nil
}
