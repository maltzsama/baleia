package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/maltzsama/baleia/internal/datapolicy"
	"github.com/maltzsama/baleia/internal/store/model"
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
// enabled or not, in its draft or in the published revision: a policy
// deleted from the draft stays in force until the next publish.
func (r *DataPolicies) CatalogHasPolicies(ctx context.Context, catalogID uuid.UUID) (bool, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.DataPolicy{}).
		Where("catalog_id = ?", catalogID.String()).Limit(1).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	published, err := r.publishedCatalogs(ctx)
	if err != nil {
		return false, err
	}
	return published[catalogID], nil
}

// EngineServesPoliciedCatalogs reports whether an engine has a binding of a
// catalog that carries data policies, in its draft or in the published
// revision.
func (r *DataPolicies) EngineServesPoliciedCatalogs(ctx context.Context, engineID uuid.UUID) (bool, error) {
	bound := r.db.Model(&model.CatalogBinding{}).Select("catalog_id").Where("engine_id = ?", engineID.String())
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.DataPolicy{}).
		Where("catalog_id IN (?)", bound).Limit(1).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	published, err := r.publishedCatalogs(ctx)
	if err != nil || len(published) == 0 {
		return false, err
	}
	var ids []string
	if err := r.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ?", engineID.String()).Pluck("catalog_id", &ids).Error; err != nil {
		return false, err
	}
	for _, raw := range ids {
		if id, err := uuid.Parse(raw); err == nil && published[id] {
			return true, nil
		}
	}
	return false, nil
}

// publishedCatalogs returns the catalogs the published revision holds
// policies for; none when nothing is published.
func (r *DataPolicies) publishedCatalogs(ctx context.Context) (map[uuid.UUID]bool, error) {
	rev, err := r.PublishedRevision(ctx)
	if errors.Is(err, datapolicy.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var content datapolicy.Content
	if err := json.Unmarshal(rev.Content, &content); err != nil {
		return nil, fmt.Errorf("read revision %s: %w", rev.ID, err)
	}
	out := make(map[uuid.UUID]bool, len(content.Catalogs))
	for key, catalog := range content.Catalogs {
		id, err := uuid.Parse(key)
		if err != nil {
			return nil, fmt.Errorf("read revision %s: catalog %q: %w", rev.ID, key, err)
		}
		out[id] = len(catalog.Tables) > 0
	}
	return out, nil
}

// CreateRevision stores a revision under the next number without publishing
// it.
func (r *DataPolicies) CreateRevision(ctx context.Context, rev datapolicy.Revision) (datapolicy.Revision, error) {
	var out datapolicy.Revision
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, err := createRevision(tx, rev)
		out = created
		return err
	})
	return out, err
}

// PublishRevision stores a revision under the next number and moves the
// published pointer to it, in one transaction: a replica never serves a
// pointer to a revision that is not stored. The pointer must still name base,
// read under a row lock, or the call fails with datapolicy.ErrVersionConflict:
// two publishes built on the same revision cannot both land, so neither loses
// the other's change. A first publication (base uuid.Nil) inserts the pointer
// and never overwrites one: a concurrent first publication that wrote it
// first, or took the revision number first, makes it fail the same way. A
// publish or a rollback first locks the catalogs it holds policies for
// (lockPoliciedCatalogs), then the engines they are bound to, and fails with
// datapolicy.ErrPoliciesUnenforced when one does not enforce data policies
// (requireEnforcingEngines).
func (r *DataPolicies) PublishRevision(ctx context.Context, rev datapolicy.Revision, base uuid.UUID) (datapolicy.Revision, error) {
	var out datapolicy.Revision
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var policied []string
		if rev.Reason == datapolicy.ReasonPublish || rev.Reason == datapolicy.ReasonRollback {
			catalogs, err := lockPoliciedCatalogs(tx, rev.Content)
			if err != nil {
				return err
			}
			policied = catalogs
		}
		read := tx.Where("id = ?", true)
		if base != uuid.Nil {
			read = read.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate})
		}
		var pointers []model.DataPolicyPointer
		if err := read.Find(&pointers).Error; err != nil {
			return err
		}
		current := ""
		if len(pointers) > 0 {
			current = string(pointers[0].RevisionID)
		}
		want := ""
		if base != uuid.Nil {
			want = base.String()
		}
		if current != want {
			return datapolicy.ErrVersionConflict
		}
		if err := requireEnforcingEngines(tx, policied); err != nil {
			return err
		}
		created, err := createRevision(tx, rev)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if base == uuid.Nil {
			pointer := model.DataPolicyPointer{ID: true, RevisionID: model.UUID(created.ID.String()), UpdatedAt: now}
			if err := tx.Create(&pointer).Error; err != nil {
				return fmt.Errorf("create the published pointer: %w", err)
			}
		} else if err := tx.Model(&model.DataPolicyPointer{}).Where("id = ?", true).
			Updates(map[string]any{"revision_id": model.UUID(created.ID.String()), "updated_at": now}).Error; err != nil {
			return fmt.Errorf("move the published pointer: %w", err)
		}
		out = created
		return nil
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return datapolicy.Revision{}, datapolicy.ErrVersionConflict
	}
	return out, err
}

// lockPoliciedCatalogs locks, in id order, the rows of the catalogs content
// holds policies for and returns their ids. A new binding locks its catalog
// before it checks the published revision, so a binding and the publish of
// its catalog's policies cannot interleave (ADR 0028). It runs before any
// other read of the transaction, so on MySQL and MariaDB the snapshot the
// later reads use already holds every binding that committed first. A
// re-issue skips it: it never adds a policy, and refusing it would let the
// lease lapse for every user.
func lockPoliciedCatalogs(tx *gorm.DB, raw []byte) ([]string, error) {
	var content datapolicy.Content
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, fmt.Errorf("datapolicy revision: %w", err)
	}
	catalogs := make([]string, 0, len(content.Catalogs))
	for key, catalog := range content.Catalogs {
		if len(catalog.Tables) > 0 {
			catalogs = append(catalogs, key)
		}
	}
	if len(catalogs) == 0 {
		return nil, nil
	}
	var rows []model.Catalog
	if err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Where("id IN ?", catalogs).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return catalogs, nil
}

// requireEnforcingEngines locks, in id order, every engine bound to one of
// catalogs, and fails with datapolicy.ErrPoliciesUnenforced when one does not
// enforce data policies. Turning an engine's data_policies off takes the same
// row lock before it checks the published revision, so the two cannot
// interleave (ADR 0028).
func requireEnforcingEngines(tx *gorm.DB, catalogs []string) error {
	if len(catalogs) == 0 {
		return nil
	}
	var engineIDs []string
	if err := tx.Model(&model.CatalogBinding{}).Where("catalog_id IN ?", catalogs).
		Pluck("engine_id", &engineIDs).Error; err != nil {
		return err
	}
	if len(engineIDs) == 0 {
		return nil
	}
	var engines []model.Engine
	if err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Where("id IN ?", engineIDs).Order("id").Find(&engines).Error; err != nil {
		return err
	}
	for _, e := range engines {
		if !e.DataPolicies {
			return datapolicy.ErrPoliciesUnenforced
		}
	}
	return nil
}

// PublishedRevision returns the revision the pointer names, or
// datapolicy.ErrNotFound when nothing was ever published.
func (r *DataPolicies) PublishedRevision(ctx context.Context) (datapolicy.Revision, error) {
	var pointer model.DataPolicyPointer
	err := r.db.WithContext(ctx).Where("id = ?", true).First(&pointer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return datapolicy.Revision{}, datapolicy.ErrNotFound
	}
	if err != nil {
		return datapolicy.Revision{}, err
	}
	id, err := uuid.Parse(string(pointer.RevisionID))
	if err != nil {
		return datapolicy.Revision{}, fmt.Errorf("published pointer: %w", err)
	}
	return r.GetRevision(ctx, id)
}

// createRevision takes the next number in tx and stores rev under it. A
// concurrent writer that took the number first makes this one fail on the
// unique number rather than share it.
func createRevision(tx *gorm.DB, rev datapolicy.Revision) (datapolicy.Revision, error) {
	if !json.Valid(rev.Content) {
		return datapolicy.Revision{}, errors.New("datapolicy revision: content is not JSON")
	}
	var last []int64
	if err := tx.Model(&model.DataPolicyRevision{}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "number"}, Desc: true}).
		Limit(1).Pluck("number", &last).Error; err != nil {
		return datapolicy.Revision{}, err
	}
	rev.Number = 1
	if len(last) > 0 {
		rev.Number = last[0] + 1
	}
	rev.CreatedAt = time.Now().UTC()
	row := toRevisionRow(rev)
	if err := tx.Create(&row).Error; err != nil {
		return datapolicy.Revision{}, err
	}
	return rev, nil
}

// RecordEngineStatus stores an engine's latest OPA status report, replacing
// the previous one.
func (r *DataPolicies) RecordEngineStatus(ctx context.Context, s datapolicy.EngineStatus) error {
	row := model.DataPolicyEngineStatus{
		EngineID:       model.UUID(s.EngineID.String()),
		ReportedAt:     s.ReportedAt.UTC(),
		ActiveRevision: model.Text(truncateRunes(s.ActiveRevision, 64)),
		PolicyDigest:   model.Text(truncateRunes(s.PolicyDigest, 64)),
		Code:           model.Text(truncateRunes(s.Code, 64)),
		Message:        model.Text(truncateRunes(s.Message, 1024)),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "engine_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"reported_at", "active_revision", "policy_digest", "code", "message"}),
	}).Create(&row).Error
}

// ListEngineStatuses returns every engine's latest OPA status report.
func (r *DataPolicies) ListEngineStatuses(ctx context.Context) ([]datapolicy.EngineStatus, error) {
	var rows []model.DataPolicyEngineStatus
	if err := r.db.WithContext(ctx).Order("engine_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]datapolicy.EngineStatus, 0, len(rows))
	for _, row := range rows {
		id, err := uuid.Parse(string(row.EngineID))
		if err != nil {
			return nil, fmt.Errorf("engine status %q: %w", row.EngineID, err)
		}
		out = append(out, datapolicy.EngineStatus{
			EngineID: id, ReportedAt: row.ReportedAt.UTC(), ActiveRevision: string(row.ActiveRevision),
			PolicyDigest: string(row.PolicyDigest), Code: string(row.Code), Message: string(row.Message),
		})
	}
	return out, nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
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

// ListSubjectSources returns every active account with its teams and the
// identity-provider groups of its latest sign-in, for the bundle's subject
// map (ADR 0028). Suspended accounts are left out.
func (r *DataPolicies) ListSubjectSources(ctx context.Context) ([]datapolicy.SubjectSource, error) {
	var users []model.User
	if err := r.db.WithContext(ctx).Select("id", "email", "kind").
		Where("status = ?", "active").Order("id").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("list active users: %w", err)
	}
	var members []model.TeamMember
	if err := r.db.WithContext(ctx).Select("user_id", "team_id").Find(&members).Error; err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	var groups []model.UserIDPGroup
	if err := r.db.WithContext(ctx).Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("list identity-provider groups: %w", err)
	}
	teamsOf := map[model.UUID][]string{}
	for _, m := range members {
		teamsOf[m.UserID] = append(teamsOf[m.UserID], string(m.TeamID))
	}
	groupsOf := map[model.UUID][]string{}
	for _, g := range groups {
		groupsOf[g.UserID] = append(groupsOf[g.UserID], string(g.Name))
	}
	out := make([]datapolicy.SubjectSource, 0, len(users))
	for _, u := range users {
		out = append(out, datapolicy.SubjectSource{
			UserID:         PGUUID(string(u.ID)),
			Email:          string(u.Email),
			ServiceAccount: u.Kind == "service_account",
			Teams:          teamsOf[u.ID],
			Groups:         groupsOf[u.ID],
		})
	}
	return out, nil
}
