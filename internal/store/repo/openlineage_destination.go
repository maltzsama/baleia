package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/openlineage"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OpenLineageDestinations persists workspace event destinations.
type OpenLineageDestinations struct{ db *gorm.DB }

func NewOpenLineageDestinations(db *gorm.DB) *OpenLineageDestinations {
	return &OpenLineageDestinations{db: db}
}

func (r *OpenLineageDestinations) Get(ctx context.Context, workspaceID string) (model.OpenLineageDestination, error) {
	var row model.OpenLineageDestination
	err := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, db.ErrNoRows
	}
	return row, err
}

func (r *OpenLineageDestinations) Upsert(ctx context.Context, row model.OpenLineageDestination, updateToken bool) (model.OpenLineageDestination, error) {
	row.UpdatedAt = time.Now().UTC()
	fields := []string{"endpoint", "namespace", "updated_by", "updated_at"}
	if updateToken {
		fields = append(fields, "token_enc")
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "workspace_id"}}, DoUpdates: clause.AssignmentColumns(fields)}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("workspace_id = ?", row.WorkspaceID).First(&row).Error; err != nil {
			return err
		}
		return openlineage.ValidateBearerTransport(row.Endpoint, row.TokenEnc != "")
	})
	return row, err
}

func (r *OpenLineageDestinations) Delete(ctx context.Context, workspaceID string) error {
	return r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).Delete(&model.OpenLineageDestination{}).Error
}
