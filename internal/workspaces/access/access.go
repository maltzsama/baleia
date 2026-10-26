// Package access answers what a caller can reach: whether a user, or an
// agent acting for one, may use a workspace (#59), the workspace itself, its
// query targets (#16), the principals it can grant to (#113), a user's Trino
// identity, and the navigation flags and global actions of /auth/me (#104).
package access

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/trinoidentity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Workspaces reads membership, workspaces and targets; *db.Queries
// implements it.
type Workspaces interface {
	CheckWorkspaceAccess(ctx context.Context, arg db.CheckWorkspaceAccessParams) (bool, error)
	AgentWorkspaceBound(ctx context.Context, arg db.AgentWorkspaceBoundParams) (bool, error)
	GetWorkspaceByID(ctx context.Context, id pgtype.UUID) (db.Workspace, error)
	ListWorkspaceTargets(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListWorkspaceTargetsRow, error)
}

// Principals reads users and teams; *db.Queries implements it.
type Principals interface {
	ListWorkspacePrincipalUsers(ctx context.Context, arg db.ListWorkspacePrincipalUsersParams) ([]db.ListWorkspacePrincipalUsersRow, error)
	ListTeamsByUser(ctx context.Context, userID pgtype.UUID) ([]db.Team, error)
	ListTeamMembers(ctx context.Context, teamID pgtype.UUID) ([]db.ListTeamMembersRow, error)
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
	UserOwnsTeam(ctx context.Context, userID pgtype.UUID) (bool, error)
}

// Platform reads what the navigation flags depend on; *db.Queries
// implements it.
type Platform interface {
	GetDefaultTrinoCluster(ctx context.Context) (db.TrinoCluster, error)
	ListEnabledIcebergCatalogs(ctx context.Context) ([]db.ListEnabledIcebergCatalogsRow, error)
}

// Store is Workspaces, Principals and Platform.
type Store interface {
	Workspaces
	Principals
	Platform
}

// ErrNotFound: the workspace does not exist or the caller may not use it.
var ErrNotFound = errors.New("workspace not found")

// Service answers access questions.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// Actor is who asks: a user, or an agent acting for its owner when Agent is
// valid.
type Actor struct {
	User  pgtype.UUID
	Agent pgtype.UUID
}

// Check returns ErrNotFound unless the user is a member of ws and, for an
// agent, the agent is bound to ws: an agent acts only in the workspaces it is
// bound to, even though its owner remains a member of them (#59).
func (s *Service) Check(ctx context.Context, ws pgtype.UUID, a Actor) error {
	ok, err := s.store.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{ID: ws, UserID: a.User})
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if !a.Agent.Valid {
		return nil
	}
	bound, err := s.store.AgentWorkspaceBound(ctx, db.AgentWorkspaceBoundParams{AgentID: a.Agent, WorkspaceID: ws})
	if err != nil {
		return err
	}
	if !bound {
		return ErrNotFound
	}
	return nil
}

// Workspace returns a workspace, without checking access.
func (s *Service) Workspace(ctx context.Context, id pgtype.UUID) (db.Workspace, error) {
	w, err := s.store.GetWorkspaceByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Workspace{}, ErrNotFound
	}
	return w, err
}

// Targets lists every binding a workspace may query, usable or not.
func (s *Service) Targets(ctx context.Context, ws pgtype.UUID) ([]db.ListWorkspaceTargetsRow, error) {
	return s.store.ListWorkspaceTargets(ctx, ws)
}

// Principal is a user or team content can be shared with.
type Principal struct {
	Kind  string // "user" or "team"
	ID    pgtype.UUID
	Name  string
	Email string // users only
	// Members counts a team's members.
	Members int
}

// PrincipalFilter narrows Principals. Kind is "", "user" or "team"; Q
// matches names and e-mails case-insensitively.
type PrincipalFilter struct {
	Kind  string
	Q     string
	Limit int
}

// Principals lists up to f.Limit members of ws, then the caller's teams.
func (s *Service) Principals(ctx context.Context, ws, caller pgtype.UUID, f PrincipalFilter) ([]Principal, error) {
	q := strings.ToLower(strings.TrimSpace(f.Q))
	match := func(values ...string) bool {
		if q == "" {
			return true
		}
		for _, v := range values {
			if strings.Contains(strings.ToLower(v), q) {
				return true
			}
		}
		return false
	}
	out := make([]Principal, 0, f.Limit)
	if f.Kind == "" || f.Kind == "user" {
		users, err := s.store.ListWorkspacePrincipalUsers(ctx, db.ListWorkspacePrincipalUsersParams{WorkspaceID: ws, Lim: limit32(f.Limit)})
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if !match(u.Name, u.Email) {
				continue
			}
			out = append(out, Principal{Kind: "user", ID: u.ID, Name: u.Name, Email: u.Email})
			if len(out) >= f.Limit {
				return out, nil
			}
		}
	}
	if f.Kind == "" || f.Kind == "team" {
		teams, err := s.store.ListTeamsByUser(ctx, caller)
		if err != nil {
			return nil, err
		}
		for _, t := range teams {
			if !match(t.Name) {
				continue
			}
			members, err := s.store.ListTeamMembers(ctx, t.ID)
			if err != nil {
				continue
			}
			out = append(out, Principal{Kind: "team", ID: t.ID, Name: t.Name, Members: len(members)})
			if len(out) >= f.Limit {
				break
			}
		}
	}
	return out, nil
}

// UserName returns a user's name, empty when the user cannot be read.
func (s *Service) UserName(ctx context.Context, id pgtype.UUID) string {
	u, err := s.store.GetUserByID(ctx, id)
	if err != nil {
		return ""
	}
	return u.Name
}

// TrinoIdentity is the Trino user name of uid. mode "email" reads the user's
// e-mail; anything else derives u_<uuid> without a lookup.
func (s *Service) TrinoIdentity(ctx context.Context, mode string, uid pgtype.UUID) (string, error) {
	if mode == "email" {
		return trinoidentity.For(ctx, s.store, mode, uid)
	}
	return "u_" + uid.String(), nil
}

// RetrieversAvailable reports whether a Trino engine can host retriever
// destinations: there is a default cluster with an engine and an enabled
// Iceberg catalog.
func (s *Service) RetrieversAvailable(ctx context.Context) bool {
	cluster, err := s.store.GetDefaultTrinoCluster(ctx)
	if err != nil || !cluster.EngineID.Valid {
		return false
	}
	cats, err := s.store.ListEnabledIcebergCatalogs(ctx)
	return err == nil && len(cats) > 0
}

// OwnsTeam reports whether the user owns a team, which lets them create
// shared workspaces.
func (s *Service) OwnsTeam(ctx context.Context, user pgtype.UUID) bool {
	owns, err := s.store.UserOwnsTeam(ctx, user)
	return err == nil && owns
}

// limit32 converts a limit, saturating instead of wrapping.
func limit32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < 0:
		return 0
	}
	return int32(v)
}
