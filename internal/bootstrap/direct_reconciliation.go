package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReconcileIncompleteDirectConnections repairs rows left by the legacy
// two-request workflow. Remote validation and discovery happen before the
// transaction; the engine, native catalogs and bindings then commit together.
// A failed connection is retained and logged for operator inspection.
func ReconcileIncompleteDirectConnections(
	ctx context.Context,
	store *gorm.DB,
	kr *crypto.Keyring,
	registry *connections.Registry,
	logger *slog.Logger,
) error {
	if store == nil || registry == nil {
		return nil
	}
	var rows []model.CatalogConnection
	if err := store.WithContext(ctx).
		Where("access_mode = ? AND NOT EXISTS (?)", "direct",
			store.Model(&model.Engine{}).Select("1").
				Where("engines.connection_id = catalog_connections.id")).
		Order("name").Find(&rows).Error; err != nil {
		return fmt.Errorf("list incomplete direct connections: %w", err)
	}
	for _, row := range rows {
		if err := reconcileDirectConnection(ctx, store, kr, registry, row); err != nil {
			logger.ErrorContext(ctx, "incomplete direct connection reconciliation failed",
				"connection_id", row.ID, "name", row.Name, "err", err)
			continue
		}
		logger.InfoContext(ctx, "incomplete direct connection reconciled",
			"connection_id", row.ID, "name", row.Name)
	}
	return nil
}

func reconcileDirectConnection(
	ctx context.Context,
	store *gorm.DB,
	kr *crypto.Keyring,
	registry *connections.Registry,
	row model.CatalogConnection,
) error {
	id, err := bootstrapDBUUID(row.ID)
	if err != nil {
		return err
	}
	provider, err := registry.Get(connections.Kind(row.Type))
	if err != nil || !connections.HasRole(provider, connections.RoleDirect) {
		return fmt.Errorf("provider %q has no direct engine", row.Type)
	}
	var plain []byte
	if row.CredentialsEnc != nil {
		if kr == nil {
			return fmt.Errorf("credentials cannot be opened without a keyring")
		}
		plain, err = kr.Open(*row.CredentialsEnc, catalogs.CredentialsAAD(id))
		if err != nil {
			return fmt.Errorf("open credentials: %w", err)
		}
	}
	cfg, err := providers.FromStored(registry, provider.Kind(), id, row.URL, []byte(row.Config), plain)
	if err != nil {
		return fmt.Errorf("restore config: %w", err)
	}
	cfg, err = provider.Validate(cfg)
	if err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	remoteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := provider.Ping(remoteCtx, cfg); err != nil {
		return fmt.Errorf("check connectivity: %w", err)
	}
	natives, err := catalogs.NativeNames(remoteCtx, provider, cfg, string(row.Name))
	if err != nil {
		return fmt.Errorf("discover native catalogs: %w", err)
	}
	engineName := manage.NormalizeName(string(row.Name))
	if engineName == "" {
		return fmt.Errorf("connection name cannot form an engine name")
	}
	createdBy := db.UUID{}
	if row.CreatedBy != nil {
		createdBy, err = bootstrapDBUUID(*row.CreatedBy)
		if err != nil {
			return err
		}
	}
	return store.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked model.CatalogConnection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", row.ID).First(&locked).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.Engine{}).Where("connection_id = ?", row.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		_, _, err := manage.CreateIn(ctx, repo.New(tx), manage.NewEngine{
			Name: engineName, Kind: provider.EngineKind(), ConnectionID: id,
			CreatedBy: createdBy, Natives: natives,
		})
		return err
	})
}

func bootstrapDBUUID(id model.UUID) (db.UUID, error) {
	parsed, err := uuid.Parse(string(id))
	if err != nil {
		return db.UUID{}, fmt.Errorf("parse uuid %q: %w", id, err)
	}
	return db.UUID{Bytes: parsed, Valid: true}, nil
}
