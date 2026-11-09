package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/galileostudio/baleia/internal/workload/resourcegroups/publish"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TxEngineQueues runs fn in one transaction over a Repo bound to it, so the
// capacities, the engine flags and the published resource groups commit
// together (#635).
func (r *Repo) TxEngineQueues(ctx context.Context, fn func(*Repo) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// LockTrinoResourceGroups serializes the saves that decide who owns the Trino
// resource groups. ADR 0020 removes the advisory lock: locking every engine
// row gives the same one-owner guarantee with the model API.
func (r *Repo) LockTrinoResourceGroups(ctx context.Context) error {
	var engines []model.Engine
	return r.Users.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Order("id").Find(&engines).Error
}

// PublishTrinoResourceGroups writes a generated document as a new revision on
// the transaction's connection.
func (r *Repo) PublishTrinoResourceGroups(ctx context.Context, engineID db.UUID, environment string, doc workload.ResourceGroups) (int, error) {
	return publish.Publish(ctx, r, engineID, environment, doc)
}

// DeleteTrinoResourceGroups removes an environment's groups and selectors.
func (r *Repo) DeleteTrinoResourceGroups(ctx context.Context, environment *string) error {
	return r.Users.db.WithContext(ctx).
		Where("environment = ?", environment).
		Delete(&model.ResourceGroup{}).Error
}

// InsertTrinoResourceGroup inserts a resource group and returns its id.
func (r *Repo) InsertTrinoResourceGroup(ctx context.Context, arg db.InsertTrinoResourceGroupParams) (int64, error) {
	row := model.ResourceGroup{
		Name:                      arg.Name,
		SoftMemoryLimit:           arg.SoftMemoryLimit,
		MaxQueued:                 arg.MaxQueued,
		SoftConcurrencyLimit:      arg.SoftConcurrencyLimit,
		HardConcurrencyLimit:      arg.HardConcurrencyLimit,
		SchedulingPolicy:          arg.SchedulingPolicy,
		SchedulingWeight:          arg.SchedulingWeight,
		JmxExport:                 arg.JmxExport,
		SoftCPULimit:              arg.SoftCpuLimit,
		HardCPULimit:              arg.HardCpuLimit,
		HardPhysicalDataScanLimit: arg.HardPhysicalDataScanLimit,
		Parent:                    arg.Parent,
		Environment:               arg.Environment,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ResourceGroupID, nil
}

// InsertTrinoSelector inserts a selector.
func (r *Repo) InsertTrinoSelector(ctx context.Context, arg db.InsertTrinoSelectorParams) error {
	row := model.Selector{
		GroupID:                arg.ResourceGroupID,
		Priority:               arg.Priority,
		UserRegex:              arg.UserRegex,
		OriginalUserRegex:      arg.OriginalUserRegex,
		AuthenticatedUserRegex: arg.AuthenticatedUserRegex,
		UserGroupRegex:         arg.UserGroupRegex,
		SourceRegex:            arg.SourceRegex,
		QueryType:              arg.QueryType,
		ClientTags:             arg.ClientTags,
	}
	return r.Users.db.WithContext(ctx).Create(&row).Error
}

// NextTrinoResourceGroupRevision returns the next revision number of an
// engine. The caller holds LockTrinoResourceGroups, so no two saves collide.
func (r *Repo) NextTrinoResourceGroupRevision(ctx context.Context, engineID db.UUID) (int32, error) {
	var maxRevision *int32
	if err := r.Users.db.WithContext(ctx).Model(&model.TrinoResourceGroupRevision{}).
		Where("engine_id = ?", UUIDString(engineID)).
		Select("MAX(revision)").
		Scan(&maxRevision).Error; err != nil {
		return 0, err
	}
	if maxRevision == nil {
		return 1, nil
	}
	return *maxRevision + 1, nil
}

// InsertTrinoResourceGroupRevision records a published document.
func (r *Repo) InsertTrinoResourceGroupRevision(ctx context.Context, arg db.InsertTrinoResourceGroupRevisionParams) error {
	row := model.TrinoResourceGroupRevision{
		ID:        model.UUID(NewUUID()),
		EngineID:  model.UUID(UUIDString(arg.EngineID)),
		Revision:  arg.Revision,
		Config:    jsonOrDefault(arg.Config),
		CreatedAt: time.Now().UTC(),
	}
	return r.Users.db.WithContext(ctx).Create(&row).Error
}

// GetTrinoResourceGroupRevision returns a published document.
func (r *Repo) GetTrinoResourceGroupRevision(ctx context.Context, arg db.GetTrinoResourceGroupRevisionParams) ([]byte, error) {
	var row model.TrinoResourceGroupRevision
	err := r.Users.db.WithContext(ctx).
		Where("engine_id = ? AND revision = ?", UUIDString(arg.EngineID), arg.Revision).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, db.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	return []byte(row.Config), nil
}
