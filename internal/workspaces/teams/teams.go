// Package teams stores teams and their members (#33). The caller checks the
// caller may read a team, or is an admin for any change.
package teams

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader reads teams, members and users; *db.Queries implements it.
type Reader interface {
	ListTeams(ctx context.Context) ([]db.Team, error)
	ListTeamsByUser(ctx context.Context, userID pgtype.UUID) ([]db.Team, error)
	GetTeamByID(ctx context.Context, id pgtype.UUID) (db.Team, error)
	ListTeamMembers(ctx context.Context, teamID pgtype.UUID) ([]db.ListTeamMembersRow, error)
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
}

// Writer changes teams and members; *db.Queries implements it.
type Writer interface {
	CreateTeam(ctx context.Context, arg db.CreateTeamParams) (db.Team, error)
	DeleteTeam(ctx context.Context, id pgtype.UUID) error
	AddTeamMember(ctx context.Context, arg db.AddTeamMemberParams) (db.TeamMember, error)
	UpdateTeamMemberRole(ctx context.Context, arg db.UpdateTeamMemberRoleParams) (db.TeamMember, error)
	RemoveTeamMember(ctx context.Context, arg db.RemoveTeamMemberParams) (int64, error)
}

// Store is Reader and Writer.
type Store interface {
	Reader
	Writer
}

// Member roles.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

var (
	// ErrNotFound: no team has the id.
	ErrNotFound = errors.New("team not found")
	// ErrUserNotFound: no user has the id.
	ErrUserNotFound = errors.New("user not found")
	// ErrMemberNotFound: the user is not a member of the team.
	ErrMemberNotFound = errors.New("member not found")
	// ErrAlreadyMember: the user is already a member of the team.
	ErrAlreadyMember = errors.New("user already in team")
	// ErrInvalidRole: a member is an owner or a member.
	ErrInvalidRole = errors.New("role must be owner or member")
)

// Service manages teams.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// List returns every team to an admin, and to anyone else their teams.
func (s *Service) List(ctx context.Context, userID pgtype.UUID, admin bool) ([]db.Team, error) {
	if admin {
		return s.store.ListTeams(ctx)
	}
	return s.store.ListTeamsByUser(ctx, userID)
}

// Create stores a team.
func (s *Service) Create(ctx context.Context, name string, description *string, createdBy pgtype.UUID) (db.Team, error) {
	return s.store.CreateTeam(ctx, db.CreateTeamParams{Name: name, Description: description, CreatedBy: createdBy})
}

// Delete removes a team and, by cascade, its memberships.
func (s *Service) Delete(ctx context.Context, id pgtype.UUID) error {
	if err := s.exists(ctx, id); err != nil {
		return err
	}
	return s.store.DeleteTeam(ctx, id)
}

// Members lists a team's members with their users.
func (s *Service) Members(ctx context.Context, teamID pgtype.UUID) ([]db.ListTeamMembersRow, error) {
	return s.store.ListTeamMembers(ctx, teamID)
}

// AddMember adds a user to a team with a role.
func (s *Service) AddMember(ctx context.Context, teamID, userID pgtype.UUID, role string) (db.TeamMember, error) {
	if err := s.exists(ctx, teamID); err != nil {
		return db.TeamMember{}, err
	}
	m, err := s.store.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: teamID, UserID: userID, Role: role})
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return db.TeamMember{}, ErrAlreadyMember
	case errors.As(err, &pgErr) && pgErr.Code == "23503":
		// The team exists, so the missing reference is the user.
		return db.TeamMember{}, ErrUserNotFound
	}
	return m, err
}

// SetRole changes a member's role.
func (s *Service) SetRole(ctx context.Context, teamID, userID pgtype.UUID, role string) (db.TeamMember, error) {
	if err := s.exists(ctx, teamID); err != nil {
		return db.TeamMember{}, err
	}
	if role != RoleOwner && role != RoleMember {
		return db.TeamMember{}, ErrInvalidRole
	}
	m, err := s.store.UpdateTeamMemberRole(ctx, db.UpdateTeamMemberRoleParams{TeamID: teamID, UserID: userID, Role: role})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.TeamMember{}, ErrMemberNotFound
	}
	return m, err
}

// RemoveMember removes a user from a team.
func (s *Service) RemoveMember(ctx context.Context, teamID, userID pgtype.UUID) error {
	n, err := s.store.RemoveTeamMember(ctx, db.RemoveTeamMemberParams{TeamID: teamID, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// User returns a member's user, for the response.
func (s *Service) User(ctx context.Context, id pgtype.UUID) (db.User, error) {
	return s.store.GetUserByID(ctx, id)
}

func (s *Service) exists(ctx context.Context, id pgtype.UUID) error {
	_, err := s.store.GetTeamByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
