package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/pressly/goose/v3"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/migrate"
)

func main() {
	var command string
	flag.StringVar(&command, "cmd", "up", "command: up, down, status, reset, version (goose) or credentials (re-seal legacy credentials)")
	flag.Parse()

	cfg := config.Load()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		slog.Error("failed to set dialect", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	switch command {
	case "up":
		err = goose.UpContext(ctx, db, "db/migrations")
	case "down":
		err = goose.DownContext(ctx, db, "db/migrations")
	case "status":
		err = goose.StatusContext(ctx, db, "db/migrations")
	case "reset":
		err = goose.ResetContext(ctx, db, "db/migrations")
	case "version":
		err = goose.VersionContext(ctx, db, "db/migrations")
	case "credentials":
		err = migrateCredentials(ctx, db, cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		os.Exit(1)
	}

	if err != nil {
		slog.Error("migration error", "cmd", command, "err", err)
		os.Exit(1)
	}
}

// migrateCredentials re-seals legacy connection credentials (encrypted under
// JWT_SECRET) as v1 envelopes under BALEIA_ENCRYPTION_KEY, in one transaction.
func migrateCredentials(ctx context.Context, db *sql.DB, cfg *config.Config) error {
	if cfg.EncryptionKey == "" {
		return fmt.Errorf("BALEIA_ENCRYPTION_KEY must be set")
	}
	kr, err := crypto.NewKeyring(cfg.EncryptionKey, cfg.EncryptionKeyPrevious)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	res, err := migrate.MigrateCredentials(ctx, sqlCredentialStore{tx}, kr, cfg.JWTSecret)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info("credentials migrated", "migrated", res.Migrated, "already_v1", res.Skipped, "failed", len(res.Failed))
	for _, id := range res.Failed {
		slog.Warn("credentials left untouched: could not be decrypted with JWT_SECRET, re-enter them", "connection_id", id)
	}
	return nil
}

type sqlCredentialStore struct{ tx *sql.Tx }

func (s sqlCredentialStore) ListCredentials(ctx context.Context) ([]migrate.CredentialRow, error) {
	rows, err := s.tx.QueryContext(ctx, "SELECT id::text, credentials_enc FROM catalog_connections WHERE credentials_enc IS NOT NULL AND credentials_enc <> '' FOR UPDATE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []migrate.CredentialRow
	for rows.Next() {
		var r migrate.CredentialRow
		if err := rows.Scan(&r.ID, &r.Enc); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s sqlCredentialStore) UpdateCredentials(ctx context.Context, id, enc string) error {
	_, err := s.tx.ExecContext(ctx, "UPDATE catalog_connections SET credentials_enc = $1 WHERE id = $2::uuid", enc, id)
	return err
}
