package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SplitDualPathConnections splits connections that today have both a direct
// engine and Trino catalogs (ADR 0021 CR-17). The original connection keeps its
// ID for the Trino path — its catalogs, bindings and grants reference it — and
// a new connection takes the direct engine, with the credentials re-sealed
// under its new identity. Every split is logged. A connection whose credentials
// cannot be re-sealed is left intact and reported, so no access path is ever
// dropped. It is idempotent: the store migration backfills single-path
// connections, so only NULL access_mode rows with both paths are considered.
func SplitDualPathConnections(ctx context.Context, store *gorm.DB, kr *crypto.Keyring, logger *slog.Logger) error {
	if store == nil || kr == nil {
		return nil
	}
	var rows []model.CatalogConnection
	if err := store.WithContext(ctx).Where("access_mode IS NULL").Order("name").Find(&rows).Error; err != nil {
		return fmt.Errorf("list connections: %w", err)
	}
	for _, orig := range rows {
		var engine model.Engine
		err := store.WithContext(ctx).
			Where("connection_id = ? AND class = 'direct'", orig.ID).First(&engine).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue // not a direct path
		}
		if err != nil {
			return fmt.Errorf("read direct engine of %s: %w", orig.ID, err)
		}
		var catalogCount int64
		if err := store.WithContext(ctx).Model(&model.Catalog{}).
			Where("connection_id = ? AND origin = 'source'", orig.ID).Count(&catalogCount).Error; err != nil {
			return fmt.Errorf("count catalogs of %s: %w", orig.ID, err)
		}
		if catalogCount == 0 {
			continue // direct-only; the store migration already set its mode
		}
		newID, err := splitDualPathConnection(ctx, store, kr, orig, engine)
		if err != nil {
			// Never drop a path: leave the connection intact and report it.
			logger.WarnContext(ctx, "dual-path connection could not be split; left intact",
				"connection_id", orig.ID, "name", orig.Name, "err", err)
			continue
		}
		logger.InfoContext(ctx, "dual-path connection split",
			"original_id", orig.ID, "original_mode", "trino",
			"direct_connection_id", newID, "name", orig.Name)
	}
	return nil
}

// splitDualPathConnection creates the direct connection, repoints the direct
// engine to it and marks the original as the Trino path, in one transaction.
func splitDualPathConnection(ctx context.Context, store *gorm.DB, kr *crypto.Keyring, orig model.CatalogConnection, engine model.Engine) (model.UUID, error) {
	newID := model.UUID(uuid.NewString())
	newAAD, err := aadFor(newID)
	if err != nil {
		return "", err
	}
	var newCreds *string
	if orig.CredentialsEnc != nil {
		origAAD, err := aadFor(orig.ID)
		if err != nil {
			return "", err
		}
		plain, err := kr.Open(*orig.CredentialsEnc, origAAD)
		if err != nil {
			return "", fmt.Errorf("%w: %w", catalogs.ErrCredentialsUnreadable, err)
		}
		sealed, err := kr.Seal(plain, newAAD)
		if err != nil {
			return "", fmt.Errorf("seal credentials: %w", err)
		}
		newCreds = &sealed
	}
	now := time.Now().UTC()
	direct := "direct"
	trino := "trino"
	txErr := store.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		name, err := uniqueDirectName(tx, orig.Name)
		if err != nil {
			return err
		}
		row := model.CatalogConnection{
			ID:                  newID,
			Name:                name,
			Type:                orig.Type,
			AccessMode:          &direct,
			URL:                 orig.URL,
			Config:              orig.Config,
			CredentialsEnc:      newCreds,
			CredentialsReadOnly: orig.CredentialsReadOnly,
			CreatedBy:           orig.CreatedBy,
			CreatedAt:           now,
			UpdatedAt:           now,
			Host:                orig.Host,
			Port:                orig.Port,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("create direct connection: %w", err)
		}
		// Native catalogs belong to the direct engine, which is about to point
		// at the new connection; keep them with the engine instead of orphaning
		// them on the Trino connection. Source catalogs stay on the original.
		if err := tx.Model(&model.Catalog{}).
			Where("connection_id = ? AND origin = 'native'", orig.ID).
			Update("connection_id", newID).Error; err != nil {
			return fmt.Errorf("move native catalogs: %w", err)
		}
		if err := tx.Model(&model.Engine{}).Where("id = ?", engine.ID).
			Update("connection_id", newID).Error; err != nil {
			return fmt.Errorf("repoint direct engine: %w", err)
		}
		if err := tx.Model(&model.CatalogConnection{}).Where("id = ?", orig.ID).
			Update("access_mode", trino).Error; err != nil {
			return fmt.Errorf("set original access mode: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return "", txErr
	}
	return newID, nil
}

// aadFor is the credential additional-authenticated-data of a connection id.
func aadFor(id model.UUID) (string, error) {
	parsed, err := uuid.Parse(string(id))
	if err != nil {
		return "", fmt.Errorf("parse connection id %q: %w", id, err)
	}
	return catalogs.CredentialsAAD(db.UUID{Bytes: parsed, Valid: true}), nil
}

// uniqueDirectName returns the original name plus a "(direct)" suffix, adding a
// counter when that name is already taken so the unique index is never hit.
func uniqueDirectName(tx *gorm.DB, orig model.Text) (model.Text, error) {
	base := string(orig)
	for i := 0; ; i++ {
		candidate := model.Text(base + " (direct)")
		if i > 0 {
			candidate = model.Text(fmt.Sprintf("%s (direct %d)", base, i+1))
		}
		var count int64
		if err := tx.Model(&model.CatalogConnection{}).Where("name = ?", candidate).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
}
