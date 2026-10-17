// Package content owns the writes to saved content (#34, ADR 0013). Every
// create, update, delete, restore and import of a saved query or a semantic
// metric writes exactly one content version in the same transaction as the
// entity; a no-op update writes none.
//
// The service is the only writer: handlers must not call UpdateSavedQuery /
// DeleteSavedQuery / the metric equivalents on db.Queries directly.
package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Entity types and snapshot schemas.
const (
	SavedQueryV1     = "saved_query/v1"
	SemanticMetricV1 = "semantic_metric/v1"

	EntitySavedQuery     = "saved_query"
	EntitySemanticMetric = "semantic_metric"
)

var (
	// ErrNotFound means the entity or version does not exist.
	ErrNotFound = errors.New("content not found")
	// ErrNameConflict means the restore name/slug is already taken.
	ErrNameConflict = errors.New("content name conflict")
	// ErrSpaceMissing means the snapshot's space no longer exists and the
	// caller did not choose another one.
	ErrSpaceMissing = errors.New("space no longer exists")
)

// Service writes content and its versions in one transaction.
type Service struct {
	pool *pgxpool.Pool
	q    db.Querier
}

// New returns a Service over a pool and its queries.
func New(pool *pgxpool.Pool, q db.Querier) *Service { return &Service{pool: pool, q: q} }

// WithQuerier returns a Service that writes through q and never opens its own
// transaction; the bundle importer uses it to make the whole import atomic.
func (s *Service) WithQuerier(q db.Querier) *Service { return &Service{q: q} }

func (s *Service) inTx(ctx context.Context, fn func(db.Querier) error) error {
	if s.pool == nil {
		return fn(s.q)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ── snapshots ───────────────────────────────────────────────────────────────────

// SavedQuerySnapshot is the on-disk shape of a saved query version.
type SavedQuerySnapshot struct {
	Schema          string     `json:"schema"`
	Slug            string     `json:"slug"`
	Name            string     `json:"name"`
	SQL             string     `json:"sql"`
	FolderID        *uuid.UUID `json:"folder_id"`
	SpaceID         uuid.UUID  `json:"space_id"`
	TargetBindingID *uuid.UUID `json:"target_binding_id"`
	TargetSchema    *string    `json:"target_schema"`
}

// SemanticMetricSnapshot is the on-disk shape of a metric version.
type SemanticMetricSnapshot struct {
	Schema        string          `json:"schema"`
	Slug          string          `json:"slug"`
	Label         string          `json:"label"`
	Description   string          `json:"description"`
	Certified     bool            `json:"certified"`
	SpaceID       uuid.UUID       `json:"space_id"`
	CatalogID     uuid.UUID       `json:"catalog_id"`
	Namespace     []string        `json:"namespace"`
	Table         string          `json:"table"`
	BindingID     *uuid.UUID      `json:"binding_id"`
	Definition    json.RawMessage `json:"definition"`
	FormatVersion int             `json:"format_version"`
	OwnerUserID   *uuid.UUID      `json:"owner_user_id"`
}

func savedQuerySnapshot(q db.SavedQuery) ([]byte, error) {
	s := SavedQuerySnapshot{
		Schema: SavedQueryV1, Slug: q.Slug, Name: q.Name, SQL: q.Sql,
		SpaceID: uuid.UUID(q.SpaceID.Bytes), TargetSchema: q.TargetSchema,
	}
	if q.FolderID.Valid {
		id := uuid.UUID(q.FolderID.Bytes)
		s.FolderID = &id
	}
	if q.TargetBindingID.Valid {
		id := uuid.UUID(q.TargetBindingID.Bytes)
		s.TargetBindingID = &id
	}
	return json.Marshal(s)
}

func metricSnapshot(m db.SemanticMetric) ([]byte, error) {
	s := SemanticMetricSnapshot{
		Schema: SemanticMetricV1, Slug: m.Slug, Label: m.Label, Description: m.Description,
		Certified: m.Certified, SpaceID: uuid.UUID(m.SpaceID.Bytes),
		CatalogID: uuid.UUID(m.CatalogID.Bytes), Namespace: m.Namespace,
		Table: m.TableName, Definition: m.Definition, FormatVersion: int(m.FormatVersion),
	}
	if m.BindingID.Valid {
		id := uuid.UUID(m.BindingID.Bytes)
		s.BindingID = &id
	}
	if m.OwnerUserID.Valid {
		id := uuid.UUID(m.OwnerUserID.Bytes)
		s.OwnerUserID = &id
	}
	return json.Marshal(s)
}

func ParseSavedQuerySnapshot(raw []byte) (SavedQuerySnapshot, error) {
	var s SavedQuerySnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return SavedQuerySnapshot{}, fmt.Errorf("saved_query snapshot: %w", err)
	}
	return s, nil
}

func ParseMetricSnapshot(raw []byte) (SemanticMetricSnapshot, error) {
	var s SemanticMetricSnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return SemanticMetricSnapshot{}, fmt.Errorf("semantic_metric snapshot: %w", err)
	}
	return s, nil
}

// ── saved queries ───────────────────────────────────────────────────────────────

// CreateSavedQuery creates a query and its version 1.
func (s *Service) CreateSavedQuery(ctx context.Context, actor pgtype.UUID, in db.CreateSavedQueryParams) (db.SavedQuery, error) {
	var out db.SavedQuery
	err := s.inTx(ctx, func(q db.Querier) error {
		var err error
		out, err = q.CreateSavedQuery(ctx, in)
		if err != nil {
			return err
		}
		snap, err := savedQuerySnapshot(out)
		if err != nil {
			return err
		}
		_, err = q.InsertContentVersion(ctx, db.InsertContentVersionParams{
			WorkspaceID: out.WorkspaceID, SpaceID: out.SpaceID, EntityType: EntitySavedQuery,
			EntityID: out.ID, Version: 1, ChangeKind: "create", Snapshot: snap, AuthorID: actor,
		})
		return err
	})
	return out, err
}

// UpdateSavedQuery writes the change and one update version, or nothing for a
// no-op (changed=false).
func (s *Service) UpdateSavedQuery(ctx context.Context, actor pgtype.UUID, in db.UpdateSavedQueryParams) (db.SavedQuery, bool, error) {
	var out db.SavedQuery
	changed := false
	err := s.inTx(ctx, func(q db.Querier) error {
		current, err := q.GetSavedQueryForUpdate(ctx, db.GetSavedQueryForUpdateParams{ID: in.ID, WorkspaceID: in.WorkspaceID})
		if err != nil {
			return err
		}
		if !savedQueryDiffers(current, in) {
			out = current
			return nil
		}
		out, err = q.UpdateSavedQuery(ctx, in)
		if err != nil {
			return err
		}
		next, err := q.NextContentVersion(ctx, db.NextContentVersionParams{EntityType: EntitySavedQuery, EntityID: in.ID})
		if err != nil {
			return err
		}
		snap, err := savedQuerySnapshot(out)
		if err != nil {
			return err
		}
		_, err = q.InsertContentVersion(ctx, db.InsertContentVersionParams{
			WorkspaceID: out.WorkspaceID, SpaceID: out.SpaceID, EntityType: EntitySavedQuery,
			EntityID: out.ID, Version: next, ChangeKind: "update", Snapshot: snap, AuthorID: actor,
		})
		changed = true
		return err
	})
	return out, changed, err
}

// DeleteSavedQuery records the last state and then deletes the row.
func (s *Service) DeleteSavedQuery(ctx context.Context, actor pgtype.UUID, workspaceID, id pgtype.UUID) error {
	return s.inTx(ctx, func(q db.Querier) error {
		current, err := q.GetSavedQueryForUpdate(ctx, db.GetSavedQueryForUpdateParams{ID: id, WorkspaceID: workspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		next, err := q.NextContentVersion(ctx, db.NextContentVersionParams{EntityType: EntitySavedQuery, EntityID: id})
		if err != nil {
			return err
		}
		snap, err := savedQuerySnapshot(current)
		if err != nil {
			return err
		}
		if _, err := q.InsertContentVersion(ctx, db.InsertContentVersionParams{
			WorkspaceID: workspaceID, SpaceID: current.SpaceID, EntityType: EntitySavedQuery,
			EntityID: id, Version: next, ChangeKind: "delete", Snapshot: snap, AuthorID: actor,
		}); err != nil {
			return err
		}
		n, err := q.DeleteSavedQuery(ctx, db.DeleteSavedQueryParams{ID: id, WorkspaceID: workspaceID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RestoreSavedQuery writes a version's snapshot back (existing entity) or
// recreates the entity with its original id (deleted one). spaceID lets the
// caller pick a live space when the snapshot's space is gone.
func (s *Service) RestoreSavedQuery(ctx context.Context, actor pgtype.UUID, workspaceID, id pgtype.UUID, version int32, newName *string, spaceID *pgtype.UUID) (db.SavedQuery, error) {
	var out db.SavedQuery
	err := s.inTx(ctx, func(q db.Querier) error {
		v, err := q.GetContentVersion(ctx, db.GetContentVersionParams{
			WorkspaceID: workspaceID, EntityType: EntitySavedQuery, EntityID: id, Version: version,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		snap, err := ParseSavedQuerySnapshot(v.Snapshot)
		if err != nil {
			return err
		}
		targetSpace := spaceIDValue(spaceID, snap.SpaceID)
		if _, err := q.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: workspaceID, ID: targetSpace}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSpaceMissing
			}
			return err
		}
		folder := folderIfLive(ctx, q, workspaceID, snap.FolderID)
		targetBinding := uuidValue(snap.TargetBindingID)
		name := snap.Name
		if newName != nil && *newName != "" {
			name = *newName
		}

		if _, err := q.GetSavedQueryForUpdate(ctx, db.GetSavedQueryForUpdateParams{ID: id, WorkspaceID: workspaceID}); err == nil {
			out, err = q.UpdateSavedQuery(ctx, db.UpdateSavedQueryParams{
				WorkspaceID: workspaceID, ID: id, Name: name, Sql: snap.SQL, FolderID: folder,
				SpaceID: targetSpace, Slug: snap.Slug, TargetBindingID: targetBinding,
				TargetSchema: snap.TargetSchema, UpdatedBy: actor,
			})
			if err != nil {
				return uniqueAsConflict(err)
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			createdBy := actor
			if v.AuthorID.Valid {
				createdBy = v.AuthorID
			}
			out, err = q.RestoreSavedQuery(ctx, db.RestoreSavedQueryParams{
				ID: id, WorkspaceID: workspaceID, SpaceID: targetSpace, FolderID: folder,
				CreatedBy: createdBy, UpdatedBy: actor, Name: name, Slug: snap.Slug, Sql: snap.SQL,
				TargetBindingID: targetBinding, TargetSchema: snap.TargetSchema,
			})
			if err != nil {
				return uniqueAsConflict(err)
			}
		} else {
			return err
		}
		return s.writeVersion(ctx, q, out.WorkspaceID, out.SpaceID, EntitySavedQuery, id, "restore", actor, &version, func() ([]byte, error) {
			return savedQuerySnapshot(out)
		})
	})
	return out, err
}

// ImportSavedQuery updates an existing query from a bundle and records an
// 'import' version.
func (s *Service) ImportSavedQuery(ctx context.Context, actor pgtype.UUID, in db.UpdateSavedQueryParams) (db.SavedQuery, error) {
	var out db.SavedQuery
	err := s.inTx(ctx, func(q db.Querier) error {
		var err error
		out, err = q.UpdateSavedQuery(ctx, in)
		if err != nil {
			return err
		}
		return s.writeVersion(ctx, q, out.WorkspaceID, out.SpaceID, EntitySavedQuery, out.ID, "import", actor, nil, func() ([]byte, error) {
			return savedQuerySnapshot(out)
		})
	})
	return out, err
}

// ── semantic metrics ───────────────────────────────────────────────────────────

// CreateSemanticMetric creates a metric and its version 1.
func (s *Service) CreateSemanticMetric(ctx context.Context, actor pgtype.UUID, in db.CreateSemanticMetricParams) (db.SemanticMetric, error) {
	var out db.SemanticMetric
	err := s.inTx(ctx, func(q db.Querier) error {
		var err error
		out, err = q.CreateSemanticMetric(ctx, in)
		if err != nil {
			return err
		}
		snap, err := metricSnapshot(out)
		if err != nil {
			return err
		}
		_, err = q.InsertContentVersion(ctx, db.InsertContentVersionParams{
			WorkspaceID: out.WorkspaceID, SpaceID: out.SpaceID, EntityType: EntitySemanticMetric,
			EntityID: out.ID, Version: 1, ChangeKind: "create", Snapshot: snap, AuthorID: actor,
		})
		return err
	})
	return out, err
}

// UpdateSemanticMetric writes the change and one update version (none for a
// no-op).
func (s *Service) UpdateSemanticMetric(ctx context.Context, actor pgtype.UUID, in db.UpdateSemanticMetricParams) (db.SemanticMetric, bool, error) {
	var out db.SemanticMetric
	changed := false
	err := s.inTx(ctx, func(q db.Querier) error {
		current, err := q.GetSemanticMetricForUpdate(ctx, db.GetSemanticMetricForUpdateParams{ID: in.ID, WorkspaceID: in.WorkspaceID})
		if err != nil {
			return err
		}
		if !metricDiffers(current, in) {
			out = current
			return nil
		}
		out, err = q.UpdateSemanticMetric(ctx, in)
		if err != nil {
			return err
		}
		next, err := q.NextContentVersion(ctx, db.NextContentVersionParams{EntityType: EntitySemanticMetric, EntityID: in.ID})
		if err != nil {
			return err
		}
		snap, err := metricSnapshot(out)
		if err != nil {
			return err
		}
		_, err = q.InsertContentVersion(ctx, db.InsertContentVersionParams{
			WorkspaceID: out.WorkspaceID, SpaceID: out.SpaceID, EntityType: EntitySemanticMetric,
			EntityID: out.ID, Version: next, ChangeKind: "update", Snapshot: snap, AuthorID: actor,
		})
		changed = true
		return err
	})
	return out, changed, err
}

// ImportSemanticMetric updates an existing metric from a bundle and records an
// 'import' version.
func (s *Service) ImportSemanticMetric(ctx context.Context, actor pgtype.UUID, in db.UpdateSemanticMetricParams) (db.SemanticMetric, error) {
	var out db.SemanticMetric
	err := s.inTx(ctx, func(q db.Querier) error {
		var err error
		out, err = q.UpdateSemanticMetric(ctx, in)
		if err != nil {
			return err
		}
		return s.writeVersion(ctx, q, out.WorkspaceID, out.SpaceID, EntitySemanticMetric, out.ID, "import", actor, nil, func() ([]byte, error) {
			return metricSnapshot(out)
		})
	})
	return out, err
}

// DeleteSemanticMetric records the last state and deletes the row.
func (s *Service) DeleteSemanticMetric(ctx context.Context, actor pgtype.UUID, workspaceID, id pgtype.UUID) error {
	return s.inTx(ctx, func(q db.Querier) error {
		current, err := q.GetSemanticMetricForUpdate(ctx, db.GetSemanticMetricForUpdateParams{ID: id, WorkspaceID: workspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		next, err := q.NextContentVersion(ctx, db.NextContentVersionParams{EntityType: EntitySemanticMetric, EntityID: id})
		if err != nil {
			return err
		}
		snap, err := metricSnapshot(current)
		if err != nil {
			return err
		}
		if _, err := q.InsertContentVersion(ctx, db.InsertContentVersionParams{
			WorkspaceID: workspaceID, SpaceID: current.SpaceID, EntityType: EntitySemanticMetric,
			EntityID: id, Version: next, ChangeKind: "delete", Snapshot: snap, AuthorID: actor,
		}); err != nil {
			return err
		}
		n, err := q.DeleteSemanticMetric(ctx, db.DeleteSemanticMetricParams{WorkspaceID: workspaceID, ID: id})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RestoreSemanticMetric writes a version's snapshot back or recreates a deleted
// metric with its original id. The caller validates the definition first.
func (s *Service) RestoreSemanticMetric(ctx context.Context, actor pgtype.UUID, workspaceID, id pgtype.UUID, version int32, newSlug *string, spaceID *pgtype.UUID) (db.SemanticMetric, error) {
	var out db.SemanticMetric
	err := s.inTx(ctx, func(q db.Querier) error {
		v, err := q.GetContentVersion(ctx, db.GetContentVersionParams{
			WorkspaceID: workspaceID, EntityType: EntitySemanticMetric, EntityID: id, Version: version,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		snap, err := ParseMetricSnapshot(v.Snapshot)
		if err != nil {
			return err
		}
		targetSpace := spaceIDValue(spaceID, snap.SpaceID)
		if _, err := q.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: workspaceID, ID: targetSpace}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSpaceMissing
			}
			return err
		}
		slug := snap.Slug
		if newSlug != nil && *newSlug != "" {
			slug = *newSlug
		}
		binding := uuidValue(snap.BindingID)
		owner := uuidValue(snap.OwnerUserID)

		if _, err := q.GetSemanticMetricForUpdate(ctx, db.GetSemanticMetricForUpdateParams{ID: id, WorkspaceID: workspaceID}); err == nil {
			out, err = q.UpdateSemanticMetric(ctx, db.UpdateSemanticMetricParams{
				WorkspaceID: workspaceID, ID: id, SpaceID: targetSpace, Slug: slug, Label: snap.Label,
				Description: snap.Description, Certified: snap.Certified, CatalogID: uuidValue(&snap.CatalogID),
				Namespace: snap.Namespace, TableName: snap.Table, BindingID: binding,
				Definition: snap.Definition, FormatVersion: int32(snap.FormatVersion), OwnerUserID: owner,
				UpdatedBy: actor,
			})
			if err != nil {
				return uniqueAsConflict(err)
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			updatedBy := actor
			if !updatedBy.Valid {
				updatedBy = v.AuthorID
			}
			out, err = q.RestoreSemanticMetric(ctx, db.RestoreSemanticMetricParams{
				ID: id, WorkspaceID: workspaceID, SpaceID: targetSpace, Slug: slug, Label: snap.Label,
				Description: snap.Description, Certified: snap.Certified, CatalogID: uuidValue(&snap.CatalogID),
				Namespace: snap.Namespace, TableName: snap.Table, BindingID: binding,
				Definition: snap.Definition, FormatVersion: int32(snap.FormatVersion),
				OwnerUserID: owner, UpdatedBy: updatedBy,
			})
			if err != nil {
				return uniqueAsConflict(err)
			}
		} else {
			return err
		}
		return s.writeVersion(ctx, q, out.WorkspaceID, out.SpaceID, EntitySemanticMetric, id, "restore", actor, &version, func() ([]byte, error) {
			return metricSnapshot(out)
		})
	})
	return out, err
}

// ── helpers ─────────────────────────────────────────────────────────────────────

func (s *Service) writeVersion(ctx context.Context, q db.Querier, workspaceID, spaceID pgtype.UUID, entityType string, entityID pgtype.UUID, kind string, actor pgtype.UUID, restoredFrom *int32, snap func() ([]byte, error)) error {
	next, err := q.NextContentVersion(ctx, db.NextContentVersionParams{EntityType: entityType, EntityID: entityID})
	if err != nil {
		return err
	}
	raw, err := snap()
	if err != nil {
		return err
	}
	_, err = q.InsertContentVersion(ctx, db.InsertContentVersionParams{
		WorkspaceID: workspaceID, SpaceID: spaceID, EntityType: entityType, EntityID: entityID,
		Version: next, ChangeKind: kind, Snapshot: raw, AuthorID: actor, RestoredFrom: restoredFrom,
	})
	return err
}

func folderIfLive(ctx context.Context, q db.Querier, workspaceID pgtype.UUID, folder *uuid.UUID) pgtype.UUID {
	if folder == nil {
		return pgtype.UUID{}
	}
	id := pgtype.UUID{Bytes: *folder, Valid: true}
	if _, err := q.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: id, WorkspaceID: workspaceID}); err != nil {
		return pgtype.UUID{}
	}
	return id
}

func spaceIDValue(chosen *pgtype.UUID, fallback uuid.UUID) pgtype.UUID {
	if chosen != nil && chosen.Valid {
		return *chosen
	}
	return pgtype.UUID{Bytes: fallback, Valid: true}
}

func uuidValue(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func savedQueryDiffers(cur db.SavedQuery, in db.UpdateSavedQueryParams) bool {
	return cur.Name != in.Name || cur.Sql != in.Sql || cur.Slug != in.Slug ||
		!sameUUID(cur.FolderID, in.FolderID) || !sameUUID(cur.SpaceID, in.SpaceID) ||
		!sameUUID(cur.TargetBindingID, in.TargetBindingID) || !sameStringPtr(cur.TargetSchema, in.TargetSchema)
}

func metricDiffers(cur db.SemanticMetric, in db.UpdateSemanticMetricParams) bool {
	return cur.Slug != in.Slug || cur.Label != in.Label || cur.Description != in.Description ||
		cur.Certified != in.Certified || !sameUUID(cur.SpaceID, in.SpaceID) ||
		!sameUUID(cur.CatalogID, in.CatalogID) || !sameStrings(cur.Namespace, in.Namespace) ||
		cur.TableName != in.TableName || !sameUUID(cur.BindingID, in.BindingID) ||
		!bytes.Equal(cur.Definition, in.Definition) || cur.FormatVersion != in.FormatVersion ||
		!sameUUID(cur.OwnerUserID, in.OwnerUserID)
}

func sameUUID(a, b pgtype.UUID) bool {
	if a.Valid != b.Valid {
		return false
	}
	return !a.Valid || a.Bytes == b.Bytes
}

func sameStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func uniqueAsConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrNameConflict
	}
	return err
}
