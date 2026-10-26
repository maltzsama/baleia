// Package spaces stores a workspace's spaces and their access lists (#131):
// listing, creating with the creator as manager, updating, deleting with the
// default-space and content rules, and setting or removing members. The
// caller checks the caller's role in the workspace or the space.
package spaces

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/db"
	slugs "github.com/galileostudio/baleia/internal/slug"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader reads spaces, their members and content counts, and the principals
// members name; *db.Queries implements it.
type Reader interface {
	ListSpacesForPrincipal(ctx context.Context, arg db.ListSpacesForPrincipalParams) ([]db.ListSpacesForPrincipalRow, error)
	GetSpace(ctx context.Context, arg db.GetSpaceParams) (db.Space, error)
	GetDefaultSpace(ctx context.Context, workspaceID pgtype.UUID) (db.Space, error)
	SpaceSlugExists(ctx context.Context, arg db.SpaceSlugExistsParams) (bool, error)
	CountSpaceContent(ctx context.Context, spaceID pgtype.UUID) (int32, error)
	CountSpaceContentKinds(ctx context.Context, spaceID pgtype.UUID) (db.CountSpaceContentKindsRow, error)
	ListSpaceMembers(ctx context.Context, spaceID pgtype.UUID) ([]db.SpaceMember, error)
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
	GetTeamByID(ctx context.Context, id pgtype.UUID) (db.Team, error)
}

// Writer changes spaces and their members; *db.Queries implements it.
type Writer interface {
	UpdateSpace(ctx context.Context, arg db.UpdateSpaceParams) (db.Space, error)
	DeleteSpace(ctx context.Context, arg db.DeleteSpaceParams) (int64, error)
	SetSpaceMember(ctx context.Context, arg db.SetSpaceMemberParams) (db.SpaceMember, error)
	DeleteSpaceMember(ctx context.Context, arg db.DeleteSpaceMemberParams) (int64, error)
}

// Store is Reader and Writer.
type Store interface {
	Reader
	Writer
}

// TxStore creates a space with its first manager; db.Querier implements it.
type TxStore interface {
	CreateSpace(ctx context.Context, arg db.CreateSpaceParams) (db.Space, error)
	SetSpaceMember(ctx context.Context, arg db.SetSpaceMemberParams) (db.SpaceMember, error)
}

// Tx runs fn in one transaction.
type Tx func(ctx context.Context, fn func(TxStore) error) error

// MaxName is the longest space name, in characters.
const MaxName = 120

var (
	// ErrNotFound: the workspace has no space with the id.
	ErrNotFound = errors.New("space not found")
	// ErrNameTaken: another space of the workspace has the name.
	ErrNameTaken = errors.New("a space with this name already exists in this workspace")
	// ErrDefaultSpace: the default space cannot be deleted.
	ErrDefaultSpace = errors.New("the default space cannot be deleted")
	// ErrNotEmpty: the space has content and the delete was not forced.
	ErrNotEmpty = errors.New("the space is not empty; pass force=true to delete it and its content")
	// ErrUserNotFound: no user has the member's id.
	ErrUserNotFound = errors.New("user not found")
	// ErrTeamNotFound: no team has the member's id.
	ErrTeamNotFound = errors.New("team not found")
	// ErrMemberNotFound: the principal is not on the space's access list.
	ErrMemberNotFound = errors.New("space member not found")
)

// InvalidError says which field was refused.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Service manages spaces.
type Service struct {
	store Store
	tx    Tx
}

// New builds the service.
func New(store Store, tx Tx) *Service { return &Service{store: store, tx: tx} }

// List returns the spaces of ws that userID may see, with their role.
func (s *Service) List(ctx context.Context, ws, userID pgtype.UUID) ([]db.ListSpacesForPrincipalRow, error) {
	return s.store.ListSpacesForPrincipal(ctx, db.ListSpacesForPrincipalParams{WorkspaceID: ws, UserID: userID})
}

// Get returns a space of ws.
func (s *Service) Get(ctx context.Context, ws, id pgtype.UUID) (db.Space, error) {
	sp, err := s.store.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: ws, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Space{}, ErrNotFound
	}
	return sp, err
}

// Default returns the default space of ws, where content without a space
// goes.
func (s *Service) Default(ctx context.Context, ws pgtype.UUID) (db.Space, error) {
	return s.store.GetDefaultSpace(ctx, ws)
}

// ContentKinds counts a space's content by kind.
func (s *Service) ContentKinds(ctx context.Context, id pgtype.UUID) (db.CountSpaceContentKindsRow, error) {
	return s.store.CountSpaceContentKinds(ctx, id)
}

// Fields are a space's editable fields. A nil field keeps the current value,
// or takes the default on create (DefaultRole viewer).
type Fields struct {
	Name        *string
	Description *string
	Visibility  *string
	DefaultRole *string
}

// Create stores a space in ws with actor as its manager.
func (s *Service) Create(ctx context.Context, ws, actor pgtype.UUID, f Fields) (db.Space, error) {
	name, visibility, defaultRole := "", "", "viewer"
	if f.Name != nil {
		name = strings.TrimSpace(*f.Name)
	}
	if f.Visibility != nil {
		visibility = *f.Visibility
	}
	if f.DefaultRole != nil {
		defaultRole = *f.DefaultRole
	}
	if err := validate(name, visibility, defaultRole); err != nil {
		return db.Space{}, err
	}
	slug, err := slugs.Unique(ctx, name, func(ctx context.Context, sl string) (bool, error) {
		return s.store.SpaceSlugExists(ctx, db.SpaceSlugExistsParams{WorkspaceID: ws, Slug: sl})
	})
	if err != nil {
		return db.Space{}, err
	}
	var space db.Space
	err = s.tx(ctx, func(q TxStore) error {
		var err error
		space, err = q.CreateSpace(ctx, db.CreateSpaceParams{
			WorkspaceID: ws, Name: name, Slug: slug, Description: f.Description,
			Visibility: visibility, DefaultRole: defaultRole, CreatedBy: actor,
		})
		if err != nil {
			return err
		}
		_, err = q.SetSpaceMember(ctx, db.SetSpaceMemberParams{
			SpaceID: space.ID, PrincipalType: "user", PrincipalID: actor, Role: "manager", GrantedBy: actor,
		})
		return err
	})
	if isUniqueViolation(err) {
		return db.Space{}, ErrNameTaken
	}
	return space, err
}

// Update applies f to a space of ws. The default space stays
// workspace-visible.
func (s *Service) Update(ctx context.Context, ws, id pgtype.UUID, f Fields) (db.Space, error) {
	current, err := s.store.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: ws, ID: id})
	if err != nil {
		return db.Space{}, err
	}
	name, description := current.Name, current.Description
	visibility, defaultRole := current.Visibility, current.DefaultRole
	if f.Name != nil {
		name = strings.TrimSpace(*f.Name)
	}
	if f.Description != nil {
		description = f.Description
	}
	if f.Visibility != nil {
		visibility = *f.Visibility
	}
	if f.DefaultRole != nil {
		defaultRole = *f.DefaultRole
	}
	if err := validate(name, visibility, defaultRole); err != nil {
		return db.Space{}, err
	}
	if current.IsDefault && visibility != "workspace" {
		return db.Space{}, &InvalidError{Msg: "the default space must stay workspace-visible"}
	}
	space, err := s.store.UpdateSpace(ctx, db.UpdateSpaceParams{
		WorkspaceID: ws, ID: id, Name: name, Description: description,
		Visibility: visibility, DefaultRole: defaultRole,
	})
	if isUniqueViolation(err) {
		return db.Space{}, ErrNameTaken
	}
	return space, err
}

// Delete removes a space of ws. The default space is never deleted, and a
// space with content only when force is set.
func (s *Service) Delete(ctx context.Context, ws, id pgtype.UUID, force bool) error {
	current, err := s.store.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: ws, ID: id})
	if err != nil {
		return err
	}
	if current.IsDefault {
		return ErrDefaultSpace
	}
	n, err := s.store.CountSpaceContent(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 && !force {
		return ErrNotEmpty
	}
	deleted, err := s.store.DeleteSpace(ctx, db.DeleteSpaceParams{WorkspaceID: ws, ID: id})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}

// Members lists a space's access list.
func (s *Service) Members(ctx context.Context, id pgtype.UUID) ([]db.SpaceMember, error) {
	return s.store.ListSpaceMembers(ctx, id)
}

// SetMember grants a user or a team a role on a space.
func (s *Service) SetMember(ctx context.Context, spaceID pgtype.UUID, principalType string, principalID pgtype.UUID, role string, grantedBy pgtype.UUID) (db.SpaceMember, error) {
	switch principalType {
	case "user":
		if _, err := s.store.GetUserByID(ctx, principalID); errors.Is(err, pgx.ErrNoRows) {
			return db.SpaceMember{}, ErrUserNotFound
		} else if err != nil {
			return db.SpaceMember{}, err
		}
	case "team":
		if _, err := s.store.GetTeamByID(ctx, principalID); errors.Is(err, pgx.ErrNoRows) {
			return db.SpaceMember{}, ErrTeamNotFound
		} else if err != nil {
			return db.SpaceMember{}, err
		}
	default:
		return db.SpaceMember{}, &InvalidError{Msg: "principal_type must be user or team"}
	}
	if role != "viewer" && role != "editor" && role != "manager" {
		return db.SpaceMember{}, &InvalidError{Msg: "role must be viewer, editor or manager"}
	}
	return s.store.SetSpaceMember(ctx, db.SetSpaceMemberParams{
		SpaceID: spaceID, PrincipalType: principalType, PrincipalID: principalID, Role: role, GrantedBy: grantedBy,
	})
}

// RemoveMember takes a principal off a space's access list.
func (s *Service) RemoveMember(ctx context.Context, spaceID pgtype.UUID, principalType string, principalID pgtype.UUID) error {
	deleted, err := s.store.DeleteSpaceMember(ctx, db.DeleteSpaceMemberParams{SpaceID: spaceID, PrincipalType: principalType, PrincipalID: principalID})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrMemberNotFound
	}
	return nil
}

func validate(name, visibility, defaultRole string) error {
	if name == "" || utf8.RuneCountInString(name) > MaxName {
		return &InvalidError{Msg: "name must be 1 to 120 characters"}
	}
	if visibility != "workspace" && visibility != "restricted" {
		return &InvalidError{Msg: "visibility must be workspace or restricted"}
	}
	if defaultRole != "viewer" && defaultRole != "editor" {
		return &InvalidError{Msg: "default_role must be viewer or editor"}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
