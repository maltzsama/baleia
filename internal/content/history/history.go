// Package history reads the versions and the trash of saved content (#34):
// saved queries, semantic metrics and semantic models. Restores go through
// content.Service. The caller authorizes the space a version belongs to.
package history

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Versions reads content versions; *db.Queries implements it.
type Versions interface {
	LatestContentVersion(ctx context.Context, arg db.LatestContentVersionParams) (db.ContentVersion, error)
	ListContentVersions(ctx context.Context, arg db.ListContentVersionsParams) ([]db.ListContentVersionsRow, error)
	GetContentVersion(ctx context.Context, arg db.GetContentVersionParams) (db.GetContentVersionRow, error)
	ListDeletedContent(ctx context.Context, arg db.ListDeletedContentParams) ([]db.ListDeletedContentRow, error)
}

// Entities finds the live entity a history belongs to; *db.Queries
// implements it.
type Entities interface {
	GetSavedQueryByID(ctx context.Context, arg db.GetSavedQueryByIDParams) (db.SavedQuery, error)
	GetSemanticMetric(ctx context.Context, arg db.GetSemanticMetricParams) (db.SemanticMetric, error)
	GetSemanticModel(ctx context.Context, arg db.GetSemanticModelParams) (db.SemanticModel, error)
}

// Store is Versions and Entities.
type Store interface {
	Versions
	Entities
}

// ErrNotFound: no entity or version matches.
var ErrNotFound = errors.New("history: not found")

// Service reads content history.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// SpaceOf is the space a history is authorized against: the live entity's,
// or, for a deleted one, its last version's.
func (s *Service) SpaceOf(ctx context.Context, ws pgtype.UUID, entityType string, id pgtype.UUID) (pgtype.UUID, error) {
	space, err := s.liveSpace(ctx, ws, entityType, id)
	if err == nil {
		return space, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	v, err := s.store.LatestContentVersion(ctx, db.LatestContentVersionParams{WorkspaceID: ws, EntityType: entityType, EntityID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, ErrNotFound
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	return v.SpaceID, nil
}

func (s *Service) liveSpace(ctx context.Context, ws pgtype.UUID, entityType string, id pgtype.UUID) (pgtype.UUID, error) {
	switch entityType {
	case content.EntitySavedQuery:
		q, err := s.store.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{ID: id, WorkspaceID: ws})
		return q.SpaceID, err
	case content.EntitySemanticMetric:
		m, err := s.store.GetSemanticMetric(ctx, db.GetSemanticMetricParams{WorkspaceID: ws, ID: id})
		return m.SpaceID, err
	case content.EntitySemanticModel:
		m, err := s.store.GetSemanticModel(ctx, db.GetSemanticModelParams{WorkspaceID: ws, ID: id})
		return m.SpaceID, err
	}
	return pgtype.UUID{}, pgx.ErrNoRows
}

// Latest is the entity's latest version number; zero when it has none.
func (s *Service) Latest(ctx context.Context, ws pgtype.UUID, entityType string, id pgtype.UUID) int32 {
	v, err := s.store.LatestContentVersion(ctx, db.LatestContentVersionParams{WorkspaceID: ws, EntityType: entityType, EntityID: id})
	if err != nil {
		return 0
	}
	return v.Version
}

// List returns a page of the entity's versions, newest first.
func (s *Service) List(ctx context.Context, ws pgtype.UUID, entityType string, id pgtype.UUID, limit, offset int32) ([]db.ListContentVersionsRow, error) {
	return s.store.ListContentVersions(ctx, db.ListContentVersionsParams{WorkspaceID: ws, EntityType: entityType, EntityID: id, Lim: limit, Off: offset})
}

// Get returns one version of the entity.
func (s *Service) Get(ctx context.Context, ws pgtype.UUID, entityType string, id pgtype.UUID, version int32) (db.GetContentVersionRow, error) {
	v, err := s.store.GetContentVersion(ctx, db.GetContentVersionParams{WorkspaceID: ws, EntityType: entityType, EntityID: id, Version: version})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetContentVersionRow{}, ErrNotFound
	}
	return v, err
}

// Trash returns a page of the workspace's deleted content, of one entity
// type when entityType is set.
func (s *Service) Trash(ctx context.Context, ws pgtype.UUID, entityType *string, limit, offset int32) ([]db.ListDeletedContentRow, error) {
	return s.store.ListDeletedContent(ctx, db.ListDeletedContentParams{WorkspaceID: ws, EntityType: entityType, Lim: limit, Off: offset})
}
