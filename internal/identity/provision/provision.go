// Package provision applies a mapped directory identity to Baleia accounts:
// the team memberships an LDAP or OIDC login grants.
package provision

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// TeamMembers writes team memberships; *db.Queries implements it.
type TeamMembers interface {
	UpsertTeamMember(ctx context.Context, arg db.UpsertTeamMemberParams) error
}

// ApplyTeams adds userID to every mapped team with its mapped role. A team
// that cannot be applied is logged and skipped: the login still succeeds.
func ApplyTeams(ctx context.Context, logger *slog.Logger, store TeamMembers, userID pgtype.UUID, teams []identity.TeamAssignment) {
	for _, t := range teams {
		var teamID pgtype.UUID
		if err := teamID.Scan(t.TeamID); err != nil {
			continue
		}
		if err := store.UpsertTeamMember(ctx, db.UpsertTeamMemberParams{
			TeamID: teamID,
			UserID: userID,
			Role:   t.TeamRole,
		}); err != nil {
			logger.WarnContext(ctx, "team mapping failed", "team_id", t.TeamID, "err", err)
		}
	}
}

// WorkspaceMembers writes group-derived workspace roles; *db.Queries
// implements it.
type WorkspaceMembers interface {
	UpsertWorkspaceMember(ctx context.Context, arg db.UpsertWorkspaceMemberParams) (int64, error)
	DeleteStaleGroupWorkspaceMembers(ctx context.Context, arg db.DeleteStaleGroupWorkspaceMembersParams) error
}

// ApplyWorkspaces sets the workspace roles the identity's groups give (#653)
// and drops the group-derived roles they no longer give. A role an admin set
// is kept. On a shared workspace the role takes effect only for a member of
// its team; team membership comes from the team mapping, applied first.
func ApplyWorkspaces(ctx context.Context, logger *slog.Logger, store WorkspaceMembers, userID pgtype.UUID, workspaces []identity.WorkspaceAssignment) {
	keep := make([]pgtype.UUID, 0, len(workspaces))
	for _, w := range workspaces {
		var wsID pgtype.UUID
		if err := wsID.Scan(w.WorkspaceID); err != nil {
			continue
		}
		n, err := store.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{WorkspaceID: wsID, UserID: userID, Role: w.Role, Source: "idp_group"})
		if err != nil {
			logger.WarnContext(ctx, "workspace mapping failed", "workspace_id", w.WorkspaceID, "err", err)
			continue
		}
		if n > 0 {
			keep = append(keep, wsID)
		}
	}
	if err := store.DeleteStaleGroupWorkspaceMembers(ctx, db.DeleteStaleGroupWorkspaceMembersParams{UserID: userID, Keep: keep}); err != nil {
		logger.WarnContext(ctx, "drop stale workspace mappings failed", "err", err)
	}
}

// Accounts finds, creates and updates directory-linked accounts;
// *db.Queries implements it.
type Accounts interface {
	GetUserByExternalID(ctx context.Context, arg db.GetUserByExternalIDParams) (db.User, error)
	CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error)
	UpdateUserRole(ctx context.Context, arg db.UpdateUserRoleParams) error
	TeamMembers
	WorkspaceMembers
}

// ErrNotProvisioned: the OIDC subject has no account and may not get one.
var ErrNotProvisioned = errors.New("provision: not provisioned")

// OIDCUser returns the account of an OIDC subject, creating it when the
// provider allows (#251): auto-create on, a verified e-mail, and an allowed
// domain. An existing e-mail account is never linked. The mapped role and
// teams are applied on every login.
func OIDCUser(ctx context.Context, logger *slog.Logger, store Accounts, providerID pgtype.UUID, cfg auth.OIDCProviderConfig, info *auth.OIDCUserInfo) (db.User, error) {
	mapped, err := identity.MapIdentity(identity.MappingInput{
		Groups:           info.Groups,
		RoleMapping:      cfg.RoleMapping,
		TeamMapping:      cfg.TeamMapping,
		WorkspaceMapping: cfg.WorkspaceMapping,
		DefaultRole:      cfg.DefaultRole,
	})
	if err != nil {
		return db.User{}, err
	}

	bySubject := db.GetUserByExternalIDParams{AuthProviderID: providerID, ExternalID: &info.Subject}
	user, err := store.GetUserByExternalID(ctx, bySubject)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return db.User{}, err
		}
		if !cfg.AutoCreate || !info.EmailVerified || len(cfg.AllowedEmailDomains) == 0 || !DomainAllowed(info.Email, cfg.AllowedEmailDomains) {
			return db.User{}, ErrNotProvisioned
		}
		user, err = store.CreateUser(ctx, db.CreateUserParams{
			Name:           info.Name,
			Email:          info.Email,
			AuthProvider:   "oidc",
			AuthProviderID: providerID,
			ExternalID:     &info.Subject,
			Role:           mapped.Role,
		})
		if err == nil {
			ApplyTeams(ctx, logger, store, user.ID, mapped.Teams)
			ApplyWorkspaces(ctx, logger, store, user.ID, mapped.Workspaces)
			return user, nil
		}
		if !isUniqueViolation(err) {
			return db.User{}, err
		}
		// A concurrent login may have created the same subject; an existing
		// e-mail account is never linked.
		if user, err = store.GetUserByExternalID(ctx, bySubject); err != nil {
			return db.User{}, ErrNotProvisioned
		}
	}

	if mapped.Role != user.Role {
		if err := store.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: user.ID, Role: mapped.Role}); err != nil {
			return db.User{}, err
		}
		user.Role = mapped.Role
	}
	ApplyTeams(ctx, logger, store, user.ID, mapped.Teams)
	ApplyWorkspaces(ctx, logger, store, user.ID, mapped.Workspaces)
	return user, nil
}

// DomainAllowed reports whether email's domain is one of domains, compared
// case-insensitively; a leading "@" in a domain is ignored.
func DomainAllowed(email string, domains []string) bool {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range domains {
		if strings.ToLower(strings.TrimPrefix(d, "@")) == domain {
			return true
		}
	}
	return false
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
