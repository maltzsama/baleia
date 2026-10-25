// Package folders organizes saved queries into folders within a space. The
// caller checks that the user may see or edit the space.
package folders

import (
	"context"
	"errors"
	"strings"

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

// Mover renames and moves folders, with their subtree; *db.Queries
// implements it.
type Mover interface {
	UpdateQueryFolder(ctx context.Context, arg db.UpdateQueryFolderParams) (db.QueryFolder, error)
	MoveQueryFolderSubtree(ctx context.Context, arg db.MoveQueryFolderSubtreeParams) error
	MoveSavedQueriesInFolderSubtree(ctx context.Context, arg db.MoveSavedQueriesInFolderSubtreeParams) error
}

// Store is Reader, Writer and Mover.
type Store interface {
	Reader
	Writer
	Mover
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
	// ErrEmptyName: a folder needs a name.
	ErrEmptyName = errors.New("name cannot be empty")
	// ErrSlugConflict: another folder in the destination space uses the slug.
	ErrSlugConflict = errors.New("another folder in the destination space uses this slug")
)

// CycleError: the new parent is the folder itself or one of its descendants.
type CycleError struct{ Msg string }

func (e *CycleError) Error() string { return e.Msg }

// Tx runs fn in one transaction over a Mover.
type Tx func(ctx context.Context, fn func(Mover) error) error

// Service manages folders.
type Service struct {
	store Store
	tx    Tx
}

// New builds the service; tx makes a move of a folder and its subtree atomic.
func New(store Store, tx Tx) *Service { return &Service{store: store, tx: tx} }

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

// Edit renames or moves a folder; nil fields keep their value. Moving to
// another space without a parent puts the folder at that space's root.
type Edit struct {
	Name *string
	Slug *string
	// SpaceID is the destination space, already authorized by the caller.
	SpaceID pgtype.UUID
	Parent  *pgtype.UUID
}

// maxDepth bounds the walk up the parents when looking for a cycle.
const maxDepth = 128

// Update applies e to folder. Moving to another space moves the folder's
// subtree and the saved queries in it too.
func (s *Service) Update(ctx context.Context, folder db.QueryFolder, e Edit) (db.QueryFolder, error) {
	name := folder.Name
	if e.Name != nil {
		name = strings.TrimSpace(*e.Name)
	}
	if name == "" {
		return db.QueryFolder{}, ErrEmptyName
	}
	folderSlug := folder.Slug
	if e.Slug != nil {
		folderSlug = *e.Slug
	}
	if !slug.Valid(folderSlug) {
		return db.QueryFolder{}, ErrInvalidSlug
	}
	moving := e.SpaceID != folder.SpaceID
	exists, err := s.store.FolderSlugExists(ctx, db.FolderSlugExistsParams{SpaceID: e.SpaceID, Slug: folderSlug})
	if err != nil {
		return db.QueryFolder{}, err
	}
	if exists && moving {
		return db.QueryFolder{}, ErrSlugConflict
	}

	parent := folder.ParentID
	if e.Parent != nil {
		parent = *e.Parent
	}
	if moving && e.Parent == nil {
		parent = pgtype.UUID{}
	}
	if parent.Valid {
		if parent == folder.ID {
			return db.QueryFolder{}, &CycleError{Msg: "a folder cannot be its own parent"}
		}
		// The parent must exist in the workspace and in the destination space.
		p, err := s.Get(ctx, folder.WorkspaceID, parent)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return db.QueryFolder{}, err
		}
		if err != nil || p.SpaceID != e.SpaceID {
			return db.QueryFolder{}, ErrParentNotFound
		}
		cur := parent
		for depth := 0; depth < maxDepth && cur.Valid; depth++ {
			if cur == folder.ID {
				return db.QueryFolder{}, &CycleError{Msg: "cannot move a folder under its own descendant"}
			}
			next, err := s.store.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: cur, WorkspaceID: folder.WorkspaceID})
			if err != nil {
				break
			}
			cur = next.ParentID
		}
	}

	// The folder, its subtree and their saved queries move together: a
	// failure halfway must not leave them in different spaces.
	var updated db.QueryFolder
	err = s.tx(ctx, func(q Mover) error {
		var err error
		updated, err = q.UpdateQueryFolder(ctx, db.UpdateQueryFolderParams{
			Name: name, Slug: &folderSlug, ParentID: parent, SpaceID: e.SpaceID, ID: folder.ID, WorkspaceID: folder.WorkspaceID,
		})
		if err != nil || !moving {
			return err
		}
		move := db.MoveQueryFolderSubtreeParams{ID: folder.ID, WorkspaceID: folder.WorkspaceID, SpaceID: e.SpaceID}
		if err := q.MoveQueryFolderSubtree(ctx, move); err != nil {
			return err
		}
		return q.MoveSavedQueriesInFolderSubtree(ctx, db.MoveSavedQueriesInFolderSubtreeParams(move))
	})
	if isUniqueViolation(err) {
		// Another folder took the slug in the destination space.
		return db.QueryFolder{}, ErrSlugConflict
	}
	if err != nil {
		return db.QueryFolder{}, err
	}
	return updated, nil
}
