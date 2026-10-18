// Package identity maps directory groups to Baleia roles and teams.
//
// OIDC, LDAP, directory reconciliation and the mapping preview all call
// MapIdentity, so a login and an admin preview can never disagree. The
// function is pure: it never reads the database, and the caller validates that
// a mapped team exists.
//
// Precedence: when an identity is in several groups that map to a role, the
// most privileged role wins (admin over member). This avoids accidentally
// demoting an administrator who is also a member of a regular group. When no
// role group matches, DefaultRole applies (member when unset).
package identity

import (
	"fmt"
	"slices"
	"strings"
)

// Platform roles. They match the users.role vocabulary.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Team roles. They match the team_members.role vocabulary.
const (
	TeamRoleOwner  = "owner"
	TeamRoleMember = "member"
)

// RoleMapping binds a directory group to a platform role.
type RoleMapping struct {
	Group string
	Role  string
}

// TeamMapping binds a directory group to a team and the role the user gets in
// it.
type TeamMapping struct {
	Group    string
	TeamID   string
	TeamRole string
}

// TeamAssignment is one team the identity belongs to.
type TeamAssignment struct {
	TeamID   string
	TeamRole string
}

// MappingInput is one identity's raw directory groups plus the provider's
// mapping rules.
type MappingInput struct {
	Groups      []string
	RoleMapping []RoleMapping
	TeamMapping []TeamMapping
	// DefaultRole applies when no role group matches: "admin" or "member".
	DefaultRole string
}

// MappingResult is the platform role and the teams to assign. Teams are sorted
// by id so previews and tests are stable.
type MappingResult struct {
	Role  string
	Teams []TeamAssignment
}

// MapIdentity derives the platform role and team assignments of an identity.
// Malformed rules (an empty group, an unknown role, an empty team id) are an
// error rather than silently ignored.
func MapIdentity(in MappingInput) (MappingResult, error) {
	groups := make(map[string]struct{}, len(in.Groups))
	for _, g := range in.Groups {
		if g = strings.TrimSpace(g); g != "" {
			groups[g] = struct{}{}
		}
	}

	defaultRole := strings.TrimSpace(in.DefaultRole)
	if defaultRole == "" {
		defaultRole = RoleMember
	}
	if !validRole(defaultRole) {
		return MappingResult{}, fmt.Errorf("identity: default role %q is not %q or %q", in.DefaultRole, RoleAdmin, RoleMember)
	}

	role := ""
	for i, m := range in.RoleMapping {
		group := strings.TrimSpace(m.Group)
		if group == "" {
			return MappingResult{}, fmt.Errorf("identity: role mapping %d has an empty group", i)
		}
		if !validRole(m.Role) {
			return MappingResult{}, fmt.Errorf("identity: role mapping %d has invalid role %q", i, m.Role)
		}
		if _, ok := groups[group]; !ok {
			continue
		}
		if role == "" || roleRank(m.Role) > roleRank(role) {
			role = m.Role
		}
	}
	if role == "" {
		role = defaultRole
	}

	teams := make(map[string]string, len(in.TeamMapping))
	for i, m := range in.TeamMapping {
		group := strings.TrimSpace(m.Group)
		teamID := strings.TrimSpace(m.TeamID)
		teamRole := strings.TrimSpace(m.TeamRole)
		if group == "" {
			return MappingResult{}, fmt.Errorf("identity: team mapping %d has an empty group", i)
		}
		if teamID == "" {
			return MappingResult{}, fmt.Errorf("identity: team mapping %d has an empty team id", i)
		}
		if !validTeamRole(teamRole) {
			return MappingResult{}, fmt.Errorf("identity: team mapping %d has invalid team role %q", i, teamRole)
		}
		if _, ok := groups[group]; !ok {
			continue
		}
		// One membership per team: the most privileged role wins.
		if prev, ok := teams[teamID]; !ok || teamRank(teamRole) > teamRank(prev) {
			teams[teamID] = teamRole
		}
	}

	out := make([]TeamAssignment, 0, len(teams))
	for id, teamRole := range teams {
		out = append(out, TeamAssignment{TeamID: id, TeamRole: teamRole})
	}
	slices.SortFunc(out, func(a, b TeamAssignment) int {
		if c := strings.Compare(a.TeamID, b.TeamID); c != 0 {
			return c
		}
		return strings.Compare(a.TeamRole, b.TeamRole)
	})
	return MappingResult{Role: role, Teams: out}, nil
}

func validRole(role string) bool { return role == RoleAdmin || role == RoleMember }

func validTeamRole(role string) bool { return role == TeamRoleOwner || role == TeamRoleMember }

// roleRank orders roles by privilege: admin > member.
func roleRank(role string) int {
	if role == RoleAdmin {
		return 1
	}
	return 0
}

// teamRank orders team roles by privilege: owner > member.
func teamRank(role string) int {
	if role == TeamRoleOwner {
		return 1
	}
	return 0
}
