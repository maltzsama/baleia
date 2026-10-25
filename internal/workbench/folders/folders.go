// Package folders organizes saved queries into folders within a space. The
// caller checks that the user may see or edit the space.
package folders

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/slug"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader reads folders; *db.Queries implements it.
type Reader interface {
	ListVisibleQueryFolders(ctx context.Context, arg db.ListVisibleQueryFoldersParams) ([]db.QueryFolder, error)
	ListAllVisibleQueryFolders(ctx context.Context, arg db.ListAllVisibleQueryFoldersParams) ([]db.QueryFolder, error)
	GetQueryFolderByID(ctx context.Context, arg db.GetQueryFolderByIDParams) (db.QueryFolder, error)
}

// Writer creates and deletes folders; *db.Queries implements it.
type Writer interface {
	FolderSlugExists(ctx context.Context, arg db.FolderSlugExistsParams) (bool, error)
	CreateQueryFolder(ctx context.Context, arg db.CreateQueryFolderParams) (db.QueryFolder, error)
	DeleteQueryFolder(ctx context.Context, arg db.DeleteQueryFolderParams) (int64, error)
}

// Store is Reader and Writer.
type Store interface {
	Reader
	Writer
}

var (
	// ErrNotFound: no folder has this id in the workspace.
	ErrNotFound = errors.New("folders: not found")
	// ErrParentNotFound: the parent folder is missing or in another space.
	ErrParentNotFound = errors.New("folders: parent folder not found")
	// ErrInvalidSlug: the slug does not match the slug grammar.
	ErrInvalidSlug = errors.New("folders: invalid slug")
	// ErrSlugTaken: the space already has a folder with this slug.
	ErrSlugTaken = errors.New("folders: slug already exists in the space")
)

// Service manages folders.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// List returns the folders the user can see in the workspace, of one space
// when spaceID is valid. With a parent, only its children; without, all.
func (s *Service) List(ctx context.Context, ws, userID, spaceID pgtype.UUID, parent *pgtype.UUID) ([]db.QueryFolder, error) {
	if parent != nil {
		return s.store.ListVisibleQueryFolders(ctx, db.ListVisibleQueryFoldersParams{
			WorkspaceID: ws, UserID: userID, SpaceID: spaceID, ParentID: *parent,
		})
	}
	return s.store.ListAllVisibleQueryFolders(ctx, db.ListAllVisibleQueryFoldersParams{
		WorkspaceID: ws, UserID: userID, SpaceID: spaceID,
	})
}

// Get returns one folder of the workspace.
func (s *Service) Get(ctx context.Context, ws, id pgtype.UUID) (db.QueryFolder, error) {
	f, err := s.store.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: id, WorkspaceID: ws})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.QueryFolder{}, ErrNotFound
	}
	return f, err
}

// Create adds a folder to a space, under parent when it is not nil. An empty
// slug is derived from the name and made unique in the space.
func (s *Service) Create(ctx context.Context, ws, spaceID pgtype.UUID, parent *pgtype.UUID, name, folderSlug string) (db.QueryFolder, error) {
	var parentID pgtype.UUID
	if parent != nil {
		p, err := s.Get(ctx, ws, *parent)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return db.QueryFolder{}, err
		}
		if err != nil || p.SpaceID != spaceID {
			return db.QueryFolder{}, ErrParentNotFound
		}
		parentID = *parent
	}

	if folderSlug == "" {
		var err error
		folderSlug, err = slug.Unique(ctx, name, func(ctx context.Context, candidate string) (bool, error) {
			return s.store.FolderSlugExists(ctx, db.FolderSlugExistsParams{SpaceID: spaceID, Slug: candidate})
		})
		if err != nil {
			return db.QueryFolder{}, err
		}
	} else if !slug.Valid(folderSlug) {
		return db.QueryFolder{}, ErrInvalidSlug
	}

	folder, err := s.store.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: ws, SpaceID: spaceID, ParentID: parentID, Name: name, Slug: folderSlug,
	})
	if isUniqueViolation(err) {
		return db.QueryFolder{}, ErrSlugTaken
	}
	return folder, err
}

// Delete removes a folder of the workspace.
func (s *Service) Delete(ctx context.Context, ws, id pgtype.UUID) error {
	deleted, err := s.store.DeleteQueryFolder(ctx, db.DeleteQueryFolderParams{WorkspaceID: ws, ID: id})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
}
