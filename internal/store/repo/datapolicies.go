package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DataPolicies persists data policies and their immutable revisions (ADR
// 0028, #1143). Revisions have no update or delete here: a revision is built
// once and never rewritten.
type DataPolicies struct{ db *gorm.DB }

// NewDataPolicies builds the data policy store.
func NewDataPolicies(db *gorm.DB) *DataPolicies { return &DataPolicies{db: db} }

// Create stores a new policy at version 1. A second policy with the same name
// on the same table fails with datapolicy.ErrNameTaken.
func (r *DataPolicies) Create(ctx context.Context, p datapolicy.Policy) (datapolicy.Policy, error) {
	now := time.Now().UTC()
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	p.Version, p.CreatedAt, p.UpdatedAt = 1, now, now
	row, err := toDataPolicyRow(p)
	if err != nil {
		return datapolicy.Policy{}, err
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return datapolicy.Policy{}, dataPolicyWriteError(err)
	}
	return fromDataPolicyRow(row)
}

// Get returns one policy, or datapolicy.ErrNotFound.
func (r *DataPolicies) Get(ctx context.Context, id uuid.UUID) (datapolicy.Policy, error) {
	var row model.DataPolicy
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return datapolicy.Policy{}, datapolicy.ErrNotFound
	}
	if err != nil {
		return datapolicy.Policy{}, err
	}
	return fromDataPolicyRow(row)
}

// ListByCatalog returns the policies of one catalog, by table and name.
func (r *DataPolicies) ListByCatalog(ctx context.Context, catalogID uuid.UUID) ([]datapolicy.Policy, error) {
	var rows []model.DataPolicy
	err := r.db.WithContext(ctx).Where("catalog_id = ?", catalogID.String()).
		Order("schema_name").Order("table_name").Order("name").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]datapolicy.Policy, 0, len(rows))
	for _, row := range rows {
		p, err := fromDataPolicyRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Update replaces a policy's authored fields and its validation, if its
// version is still expected. The target's catalog and the author do not
// change. It fails with datapolicy.ErrVersionConflict when another update
// came first, and datapolicy.ErrNotFound when the policy is gone.
func (r *DataPolicies) Update(ctx context.Context, p datapolicy.Policy, expected int64) (datapolicy.Policy, error) {
	row, err := toDataPolicyRow(p)
	if err != nil {
		return datapolicy.Policy{}, err
	}
	changes := map[string]any{
		"schema_name": row.SchemaName, "table_name": row.TableNameValue, "column_name": row.ColumnName,
		"name": row.Name, "kind": row.Kind, "mode": row.Mode, "priority": row.Priority,
		"subjects": row.Subjects, "expression": row.Expression, "template": row.Template,
		"state": row.State, "description": row.Description, "compiled": row.Compiled,
		"validation": row.Validation, "invalid_reason": row.InvalidReason, "invalid_detail": row.InvalidDetail,
		"version": expected + 1, "updated_at": time.Now().UTC(),
	}
	res := r.db.WithContext(ctx).Model(&model.DataPolicy{}).
		Where("id = ? AND version = ?", p.ID.String(), expected).
		Updates(changes)
	if res.Error != nil {
		return datapolicy.Policy{}, dataPolicyWriteError(res.Error)
	}
	if res.RowsAffected == 0 {
		if _, err := r.Get(ctx, p.ID); err != nil {
			return datapolicy.Policy{}, err
		}
		return datapolicy.Policy{}, datapolicy.ErrVersionConflict
	}
	return r.Get(ctx, p.ID)
}

// Delete removes a policy and reports whether it existed. Published revisions
// keep their copy.
func (r *DataPolicies) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&model.DataPolicy{})
	return res.RowsAffected > 0, res.Error
}

// CatalogHasPolicies reports whether a catalog carries any data policy,
// enabled or not.
func (r *DataPolicies) CatalogHasPolicies(ctx context.Context, catalogID uuid.UUID) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.DataPolicy{}).
		Where("catalog_id = ?", catalogID.String()).Limit(1).Count(&n).Error
	return n > 0, err
}

// EngineServesPoliciedCatalogs reports whether an engine has a binding of a
// catalog that carries data policies.
func (r *DataPolicies) EngineServesPoliciedCatalogs(ctx context.Context, engineID uuid.UUID) (bool, error) {
	bound := r.db.Model(&model.CatalogBinding{}).Select("catalog_id").Where("engine_id = ?", engineID.String())
	var n int64
	err := r.db.WithContext(ctx).Model(&model.DataPolicy{}).
		Where("catalog_id IN (?)", bound).Limit(1).Count(&n).Error
	return n > 0, err
}

// CreateRevision stores a revision under the next number. The number is
// taken in the same transaction; a concurrent writer that took it first makes
// this one fail on the unique number rather than share it.
func (r *DataPolicies) CreateRevision(ctx context.Context, rev datapolicy.Revision) (datapolicy.Revision, error) {
	if !json.Valid(rev.Content) {
		return datapolicy.Revision{}, errors.New("datapolicy revision: content is not JSON")
	}
	var out datapolicy.Revision
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var last []int64
		if err := tx.Model(&model.DataPolicyRevision{}).
			Order(clause.OrderByColumn{Column: clause.Column{Name: "number"}, Desc: true}).
			Limit(1).Pluck("number", &last).Error; err != nil {
			return err
		}
		rev.Number = 1
		if len(last) > 0 {
			rev.Number = last[0] + 1
		}
		rev.CreatedAt = time.Now().UTC()
		row := toRevisionRow(rev)
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		out = rev
		return nil
	})
	return out, err
}

// GetRevision returns one revision, or datapolicy.ErrNotFound.
func (r *DataPolicies) GetRevision(ctx context.Context, id uuid.UUID) (datapolicy.Revision, error) {
	var row model.DataPolicyRevision
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return datapolicy.Revision{}, datapolicy.ErrNotFound
	}
	if err != nil {
		return datapolicy.Revision{}, err
	}
	return fromRevisionRow(row)
}

// ListRevisions returns up to limit revisions, newest first.
func (r *DataPolicies) ListRevisions(ctx context.Context, limit int) ([]datapolicy.Revision, error) {
	var rows []model.DataPolicyRevision
	err := r.db.WithContext(ctx).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "number"}, Desc: true}).
		Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]datapolicy.Revision, 0, len(rows))
	for _, row := range rows {
		rev, err := fromRevisionRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, nil
}

func dataPolicyWriteError(err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return datapolicy.ErrNameTaken
	}
	return err
}

func toDataPolicyRow(p datapolicy.Policy) (model.DataPolicy, error) {
	subjects, err := json.Marshal(p.Subjects)
	if err != nil {
		return model.DataPolicy{}, fmt.Errorf("encode data policy subjects: %w", err)
	}
	return model.DataPolicy{
		ID:             model.UUID(p.ID.String()),
		CatalogID:      model.UUID(p.Target.CatalogID.String()),
		SchemaName:     model.Text(p.Target.Schema),
		TableNameValue: model.Text(p.Target.Table),
		ColumnName:     model.Text(p.Target.Column),
		Name:           model.Text(p.Name),
		Kind:           model.Text(p.Kind),
		Mode:           model.Text(p.Mode),
		Priority:       p.Priority,
		Subjects:       model.JSON(subjects),
		Expression:     p.Expression,
		Template:       model.Text(p.Template),
		State:          model.Text(p.State),
		Description:    p.Description,
		Compiled:       p.Compiled,
		Validation:     model.Text(p.Validation),
		InvalidReason:  model.Text(p.InvalidReason),
		InvalidDetail:  model.Text(p.InvalidDetail),
		Version:        p.Version,
		CreatedBy:      model.UUID(p.CreatedBy.String()),
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}, nil
}

func fromDataPolicyRow(row model.DataPolicy) (datapolicy.Policy, error) {
	var subjects []datapolicy.Subject
	if err := json.Unmarshal(row.Subjects, &subjects); err != nil {
		return datapolicy.Policy{}, fmt.Errorf("decode data policy %s subjects: %w", row.ID, err)
	}
	ids, err := parseUUIDs(string(row.ID), string(row.CatalogID), string(row.CreatedBy))
	if err != nil {
		return datapolicy.Policy{}, fmt.Errorf("data policy %s: %w", row.ID, err)
	}
	return datapolicy.Policy{
		ID:   ids[0],
		Name: string(row.Name),
		Kind: datapolicy.Kind(row.Kind),
		Target: datapolicy.Target{
			CatalogID: ids[1], Schema: string(row.SchemaName), Table: string(row.TableNameValue), Column: string(row.ColumnName),
		},
		Mode:          datapolicy.Mode(row.Mode),
		Priority:      row.Priority,
		Subjects:      subjects,
		Expression:    row.Expression,
		Template:      datapolicy.MaskTemplate(row.Template),
		State:         datapolicy.State(row.State),
		Description:   row.Description,
		Compiled:      row.Compiled,
		Validation:    datapolicy.Validation(row.Validation),
		InvalidReason: string(row.InvalidReason),
		InvalidDetail: string(row.InvalidDetail),
		Version:       row.Version,
		CreatedBy:     ids[2],
		CreatedAt:     row.CreatedAt.UTC(),
		UpdatedAt:     row.UpdatedAt.UTC(),
	}, nil
}

func toRevisionRow(rev datapolicy.Revision) model.DataPolicyRevision {
	return model.DataPolicyRevision{
		ID:        model.UUID(rev.ID.String()),
		Number:    rev.Number,
		Reason:    model.Text(rev.Reason),
		SourceID:  optionalModelUUID(rev.SourceID),
		Content:   string(rev.Content),
		Digest:    model.Text(rev.Digest),
		NotAfter:  rev.NotAfter.UTC(),
		CreatedBy: optionalModelUUID(rev.CreatedBy),
		CreatedAt: rev.CreatedAt,
	}
}

func fromRevisionRow(row model.DataPolicyRevision) (datapolicy.Revision, error) {
	id, err := uuid.Parse(string(row.ID))
	if err != nil {
		return datapolicy.Revision{}, fmt.Errorf("data policy revision id %q: %w", row.ID, err)
	}
	source, err := optionalUUID(row.SourceID)
	if err != nil {
		return datapolicy.Revision{}, fmt.Errorf("data policy revision %s source: %w", row.ID, err)
	}
	createdBy, err := optionalUUID(row.CreatedBy)
	if err != nil {
		return datapolicy.Revision{}, fmt.Errorf("data policy revision %s author: %w", row.ID, err)
	}
	return datapolicy.Revision{
		ID:        id,
		Number:    row.Number,
		Reason:    datapolicy.RevisionReason(row.Reason),
		SourceID:  source,
		Content:   []byte(row.Content),
		Digest:    string(row.Digest),
		NotAfter:  row.NotAfter.UTC(),
		CreatedBy: createdBy,
		CreatedAt: row.CreatedAt.UTC(),
	}, nil
}

func parseUUIDs(values ...string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(values))
	for _, v := range values {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("uuid %q: %w", v, err)
		}
		out = append(out, id)
	}
	return out, nil
}

func optionalModelUUID(id *uuid.UUID) *model.UUID {
	if id == nil {
		return nil
	}
	v := model.UUID(id.String())
	return &v
}

func optionalUUID(id *model.UUID) (*uuid.UUID, error) {
	if id == nil || *id == "" {
		return nil, nil
	}
	v, err := uuid.Parse(string(*id))
	if err != nil {
		return nil, err
	}
	return &v, nil
}
