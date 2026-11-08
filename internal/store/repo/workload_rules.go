package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// GetWorkloadRule returns one ordered workload rule (#539, #773).
func (r *Repo) GetWorkloadRule(ctx context.Context, id pgtype.UUID) (db.WorkloadRule, error) {
	var row model.WorkloadRule
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.WorkloadRule{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.WorkloadRule{}, err
	}
	return toDBWorkloadRule(row), nil
}

// MaxWorkloadRulePosition is the highest position in use, or 0.
func (r *Repo) MaxWorkloadRulePosition(ctx context.Context) (int32, error) {
	var row model.WorkloadRule
	err := r.Users.db.WithContext(ctx).Order("position DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return row.Position, nil
}

// CreateWorkloadRule inserts one rule and returns it.
func (r *Repo) CreateWorkloadRule(ctx context.Context, arg db.CreateWorkloadRuleParams) (db.WorkloadRule, error) {
	row := model.WorkloadRule{
		ID:        model.UUID(NewUUID()),
		Position:  arg.Position,
		Enabled:   arg.Enabled,
		Match:     jsonOrDefault(arg.Match),
		Class:     arg.Class,
		MaxClass:  arg.MaxClass,
		Weight:    arg.Weight,
		CreatedBy: modelUUIDPtr(arg.CreatedBy),
	}
	values := map[string]any{
		"id":         row.ID,
		"position":   row.Position,
		"enabled":    row.Enabled,
		"match":      row.Match,
		"class":      row.Class,
		"max_class":  row.MaxClass,
		"weight":     row.Weight,
		"created_by": row.CreatedBy,
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.WorkloadRule{}).Create(values).Error; err != nil {
		return db.WorkloadRule{}, err
	}
	// created_at/updated_at come from the database clock, and the stored row
	// is what the caller returns, so ordering and timestamps agree.
	return r.GetWorkloadRule(ctx, PGUUID(string(row.ID)))
}

// UpdateWorkloadRule replaces a rule's editable fields and stamps updated_at.
func (r *Repo) UpdateWorkloadRule(ctx context.Context, arg db.UpdateWorkloadRuleParams) (db.WorkloadRule, error) {
	res := r.Users.db.WithContext(ctx).Model(&model.WorkloadRule{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"position":   arg.Position,
			"enabled":    arg.Enabled,
			"match":      jsonOrDefault(arg.Match),
			"class":      arg.Class,
			"max_class":  arg.MaxClass,
			"weight":     arg.Weight,
			"updated_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return db.WorkloadRule{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.WorkloadRule{}, pgx.ErrNoRows
	}
	return r.GetWorkloadRule(ctx, arg.ID)
}

// DeleteWorkloadRule removes one rule and reports how many rows it deleted.
func (r *Repo) DeleteWorkloadRule(ctx context.Context, id pgtype.UUID) (int64, error) {
	res := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).Delete(&model.WorkloadRule{})
	return res.RowsAffected, res.Error
}

func toDBWorkloadRule(row model.WorkloadRule) db.WorkloadRule {
	return db.WorkloadRule{
		ID:        PGUUID(string(row.ID)),
		Position:  row.Position,
		Enabled:   row.Enabled,
		Match:     []byte(row.Match),
		Class:     row.Class,
		MaxClass:  row.MaxClass,
		Weight:    row.Weight,
		CreatedBy: uuidPtr(row.CreatedBy),
		CreatedAt: PGTime(row.CreatedAt),
		UpdatedAt: PGTime(row.UpdatedAt),
	}
}
