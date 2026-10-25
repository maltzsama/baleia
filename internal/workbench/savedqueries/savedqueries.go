// Package savedqueries manages the saved queries of a workspace: listing and
// reading them, and the rules around a write (folder, slug, unique name). The
// writes and their versions go through content.Service (#34). The caller
// checks that the user may read or edit the spaces involved.
package savedqueries

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/slug"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads saved queries; *db.Queries implements it.
type Store interface {
	ListVisibleSavedQueries(ctx context.Context, arg db.ListVisibleSavedQueriesParams) ([]db.SavedQuery, error)
	SearchVisibleSavedQueries(ctx context.Context, arg db.SearchVisibleSavedQueriesParams) ([]db.SavedQuery, error)
	ListSavedQueryVersions(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListSavedQueryVersionsRow, error)
	GetSavedQueryByID(ctx context.Context, arg db.GetSavedQueryByIDParams) (db.SavedQuery, error)
	SavedQuerySlugExists(ctx context.Context, arg db.SavedQuerySlugExistsParams) (bool, error)
}

// Folders finds a folder of the workspace; *folders.Service implements it.
type Folders interface {
	Get(ctx context.Context, ws, id pgtype.UUID) (db.QueryFolder, error)
}

// Content writes saved queries and their versions; *content.Service
// implements it.
type Content interface {
	CreateSavedQuery(ctx context.Context, actor pgtype.UUID, in db.CreateSavedQueryParams) (db.SavedQuery, error)
	UpdateSavedQuery(ctx context.Context, actor pgtype.UUID, in db.UpdateSavedQueryParams, expectedVersion *int32) (db.SavedQuery, bool, error)
	DeleteSavedQuery(ctx context.Context, actor pgtype.UUID, workspaceID, id pgtype.UUID) error
}

// Target resolves the engine target a saved query runs on: its binding and
// optional schema. It runs after every other check, so its errors come last.
type Target func(ctx context.Context) (bindingID pgtype.UUID, schema *string, err error)

var (
	// ErrNotFound: no saved query has this id in the workspace.
	ErrNotFound = errors.New("savedqueries: not found")
	// ErrFolderNotFound: the folder is missing or in another space.
	ErrFolderNotFound = errors.New("savedqueries: folder not found")
	// ErrInvalidSlug: the slug does not match the slug grammar.
	ErrInvalidSlug = errors.New("savedqueries: invalid slug")
	// ErrNameTaken: the workspace already has a saved query with this name.
	ErrNameTaken = errors.New("savedqueries: name already exists in the workspace")
)

// Item is a listed saved query and its latest content version.
type Item struct {
	Query   db.SavedQuery
	Version int32
}

// Filter narrows a listing. A valid FolderID lists that folder; otherwise
// the whole workspace is searched.
type Filter struct {
	SpaceID  pgtype.UUID
	FolderID pgtype.UUID
	Q        *string
	Limit    *int32
}

// New is a saved query to create.
type New struct {
	SpaceID pgtype.UUID
	// FolderID is the folder in SpaceID, or invalid for the root.
	FolderID pgtype.UUID
	Name     string
	// Slug is derived from the name and made unique when empty.
	Slug   string
	SQL    string
	Target Target
}

// Change is a partial update; nil fields keep their value.
type Change struct {
	Name *string
	SQL  *string
	Slug *string
	// SpaceID moves the query; the caller checked the destination space.
	SpaceID *pgtype.UUID
	// MoveToRoot clears the folder; FolderID, when set, wins over it.
	MoveToRoot bool
	FolderID   *pgtype.UUID
	// Target, when set, replaces the target.
	Target Target
	// ExpectedVersion rejects the write when another one landed first.
	ExpectedVersion *int32
}

// Service manages saved queries.
type Service struct {
	store   Store
	folders Folders
	content Content
}

// NewService builds the service.
func NewService(store Store, folders Folders, content Content) *Service {
	return &Service{store: store, folders: folders, content: content}
}

// List returns the saved queries the user can see, with their versions.
func (s *Service) List(ctx context.Context, ws, userID pgtype.UUID, f Filter) ([]Item, error) {
	var (
		queries []db.SavedQuery
		err     error
	)
	if f.FolderID.Valid {
		queries, err = s.store.ListVisibleSavedQueries(ctx, db.ListVisibleSavedQueriesParams{
			WorkspaceID: ws, UserID: userID, SpaceID: f.SpaceID, FolderID: f.FolderID, Q: f.Q, Lim: f.Limit,
		})
	} else {
		queries, err = s.store.SearchVisibleSavedQueries(ctx, db.SearchVisibleSavedQueriesParams{
			WorkspaceID: ws, UserID: userID, SpaceID: f.SpaceID, Q: f.Q, Lim: f.Limit,
		})
	}
	if err != nil {
		return nil, err
	}
	versions, err := s.store.ListSavedQueryVersions(ctx, ws)
	if err != nil {
		return nil, err
	}
	byID := make(map[[16]byte]int32, len(versions))
	for _, v := range versions {
		byID[v.EntityID.Bytes] = v.Version
	}
	out := make([]Item, 0, len(queries))
	for _, q := range queries {
		out = append(out, Item{Query: q, Version: byID[q.ID.Bytes]})
	}
	return out, nil
}

// Get returns one saved query of the workspace.
func (s *Service) Get(ctx context.Context, ws, id pgtype.UUID) (db.SavedQuery, error) {
	q, err := s.store.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{WorkspaceID: ws, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.SavedQuery{}, ErrNotFound
	}
	return q, err
}

// Create saves a query as actor.
func (s *Service) Create(ctx context.Context, actor, ws pgtype.UUID, in New) (db.SavedQuery, error) {
	if err := s.checkFolder(ctx, ws, in.FolderID, in.SpaceID); err != nil {
		return db.SavedQuery{}, err
	}
	querySlug := in.Slug
	if querySlug == "" {
		var err error
		querySlug, err = slug.Unique(ctx, in.Name, func(ctx context.Context, candidate string) (bool, error) {
			return s.store.SavedQuerySlugExists(ctx, db.SavedQuerySlugExistsParams{WorkspaceID: ws, Slug: candidate})
		})
		if err != nil {
			return db.SavedQuery{}, err
		}
	} else if !slug.Valid(querySlug) {
		return db.SavedQuery{}, ErrInvalidSlug
	}
	var (
		bindingID pgtype.UUID
		schema    *string
	)
	if in.Target != nil {
		var err error
		if bindingID, schema, err = in.Target(ctx); err != nil {
			return db.SavedQuery{}, err
		}
	}

	q, err := s.content.CreateSavedQuery(ctx, actor, db.CreateSavedQueryParams{
		WorkspaceID:     ws,
		SpaceID:         in.SpaceID,
		FolderID:        in.FolderID,
		CreatedBy:       actor,
		UpdatedBy:       actor,
		Name:            in.Name,
		Slug:            querySlug,
		Sql:             in.SQL,
		TargetBindingID: bindingID,
		TargetSchema:    schema,
	})
	if isUniqueViolation(err) {
		return db.SavedQuery{}, ErrNameTaken
	}
	return q, err
}

// Update applies change to current as actor. A version conflict is returned
// as content reports it.
func (s *Service) Update(ctx context.Context, actor pgtype.UUID, current db.SavedQuery, change Change) (db.SavedQuery, error) {
	spaceID := current.SpaceID
	if change.SpaceID != nil {
		spaceID = *change.SpaceID
	}
	name, sql := current.Name, current.Sql
	if change.Name != nil {
		name = *change.Name
	}
	if change.SQL != nil {
		sql = *change.SQL
	}
	folderID := current.FolderID
	if change.MoveToRoot {
		folderID = pgtype.UUID{}
	}
	if change.FolderID != nil {
		folderID = *change.FolderID
	}
	if err := s.checkFolder(ctx, current.WorkspaceID, folderID, spaceID); err != nil {
		return db.SavedQuery{}, err
	}
	querySlug := current.Slug
	if change.Slug != nil {
		querySlug = *change.Slug
		if !slug.Valid(querySlug) {
			return db.SavedQuery{}, ErrInvalidSlug
		}
	}
	bindingID, schema := current.TargetBindingID, current.TargetSchema
	if change.Target != nil {
		var err error
		if bindingID, schema, err = change.Target(ctx); err != nil {
			return db.SavedQuery{}, err
		}
	}

	updated, _, err := s.content.UpdateSavedQuery(ctx, actor, db.UpdateSavedQueryParams{
		WorkspaceID:     current.WorkspaceID,
		ID:              current.ID,
		Name:            name,
		Sql:             sql,
		FolderID:        folderID,
		SpaceID:         spaceID,
		Slug:            querySlug,
		TargetBindingID: bindingID,
		TargetSchema:    schema,
		UpdatedBy:       actor,
	}, change.ExpectedVersion)
	if isUniqueViolation(err) {
		return db.SavedQuery{}, ErrNameTaken
	}
	return updated, err
}

// Delete removes a saved query as actor.
func (s *Service) Delete(ctx context.Context, actor, ws, id pgtype.UUID) error {
	return s.content.DeleteSavedQuery(ctx, actor, ws, id)
}

// checkFolder accepts no folder, or a folder of the workspace in spaceID.
func (s *Service) checkFolder(ctx context.Context, ws, folderID, spaceID pgtype.UUID) error {
	if !folderID.Valid {
		return nil
	}
	f, err := s.folders.Get(ctx, ws, folderID)
	if err != nil || f.SpaceID != spaceID {
		return ErrFolderNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
}
