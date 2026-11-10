package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetBIGatewaySetting returns the global BI gateway switch or db.ErrNoRows
// when the singleton has not been created yet (treat that as enabled).
func (r *Repo) GetBIGatewaySetting(ctx context.Context) (db.BIGatewaySetting, error) {
	var row model.BIGatewaySetting
	err := r.Users.db.WithContext(ctx).Where("id = ?", true).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.BIGatewaySetting{}, db.ErrNoRows
	}
	if err != nil {
		return db.BIGatewaySetting{}, err
	}
	return toDBBIGatewaySetting(row), nil
}

// SetBIGatewayEnabled writes the singleton switch and returns it.
func (r *Repo) SetBIGatewayEnabled(ctx context.Context, arg db.SetBIGatewayEnabledParams) (db.BIGatewaySetting, error) {
	now := time.Now().UTC()
	row := model.BIGatewaySetting{
		ID:        true,
		Enabled:   arg.Enabled,
		UpdatedBy: modelUUIDPtr(arg.UpdatedBy),
		UpdatedAt: now,
	}
	if err := r.Users.db.WithContext(ctx).Select("*").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"enabled":    arg.Enabled,
			"updated_by": modelUUIDPtr(arg.UpdatedBy),
			"updated_at": now,
		}),
	}).Create(&row).Error; err != nil {
		return db.BIGatewaySetting{}, err
	}
	return r.GetBIGatewaySetting(ctx)
}

// CountRunningBIExecutionsByKey counts the running BI executions of one key.
func (r *Repo) CountRunningBIExecutionsByKey(ctx context.Context, keyID db.UUID) (int64, error) {
	if !keyID.Valid {
		return 0, nil
	}
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("origin = ? AND status = ? AND bi_key_id = ?", "bi", "running", UUIDString(keyID)).
		Count(&count).Error
	return count, err
}

// CountRunningBIExecutionsByWorkspace counts the running BI executions of one
// workspace.
func (r *Repo) CountRunningBIExecutionsByWorkspace(ctx context.Context, workspaceID db.UUID) (int64, error) {
	if !workspaceID.Valid {
		return 0, nil
	}
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("origin = ? AND status = ? AND workspace_id = ?", "bi", "running", UUIDString(workspaceID)).
		Count(&count).Error
	return count, err
}

func toDBBIGatewaySetting(s model.BIGatewaySetting) db.BIGatewaySetting {
	return db.BIGatewaySetting{
		Enabled:   s.Enabled,
		UpdatedBy: uuidPtr(s.UpdatedBy),
		UpdatedAt: PGTime(s.UpdatedAt),
	}
}
