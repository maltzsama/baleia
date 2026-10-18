package activities

import (
	"fmt"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/identity"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/jackc/pgx/v5/pgtype"
)

func testUUID(n int) pgtype.UUID {
	var u pgtype.UUID
	u.Bytes[0] = byte(n)
	u.Valid = true
	return u
}

func linked(n int, externalID, role, status string, reason *string) reconcilableUser {
	return reconcilableUser{ID: testUUID(n), ExternalID: externalID, Role: role, Status: status, SuspendedReason: reason}
}

func ptr(s string) *string { return &s }

// TestReconcile_DisabledUserSuspended: a disabled directory entry suspends the
// linked active user.
func TestReconcile_DisabledUserSuspended(t *testing.T) {
	plan := planDirectoryReconcile(
		[]identityldap.DirectoryUser{{ExternalID: "ext-1", Disabled: true}},
		[]reconcilableUser{linked(1, "ext-1", identity.RoleMember, auth.StatusActive, nil)},
		nil, nil, identity.RoleMember,
	)
	if plan.Aborted || len(plan.Suspend) != 1 {
		t.Fatalf("plan = %+v, want one suspension", plan)
	}
}

// TestReconcile_ReinstateOnlyDirectorySuspensions: a directory suspension is
// lifted when the account returns; an admin suspension survives the pass.
func TestReconcile_ReinstateOnlyDirectorySuspensions(t *testing.T) {
	dir := []identityldap.DirectoryUser{
		{ExternalID: "ext-dir", Groups: nil},
		{ExternalID: "ext-admin", Groups: nil},
	}
	users := []reconcilableUser{
		linked(1, "ext-dir", identity.RoleMember, auth.StatusSuspended, ptr("directory")),
		linked(2, "ext-admin", identity.RoleMember, auth.StatusSuspended, ptr("admin")),
	}
	plan := planDirectoryReconcile(dir, users, nil, nil, identity.RoleMember)
	if plan.Aborted {
		t.Fatal("plan aborted")
	}
	if len(plan.Reinstate) != 1 || plan.Reinstate[0].ID != testUUID(1) {
		t.Fatalf("reinstate = %+v, want only the directory suspension", plan.Reinstate)
	}
}

// TestReconcile_SafetyBrake: more than max(5, 10%) suspensions aborts the whole
// pass without changes.
func TestReconcile_SafetyBrake(t *testing.T) {
	users := make([]reconcilableUser, 0, 10)
	for i := 1; i <= 10; i++ {
		users = append(users, linked(i, fmt.Sprintf("gone-%d", i), identity.RoleMember, auth.StatusActive, nil))
	}
	plan := planDirectoryReconcile(nil, users, nil, nil, identity.RoleMember)
	if !plan.Aborted {
		t.Fatalf("plan = %+v, want an abort", plan)
	}
	if len(plan.Suspend) != 0 || len(plan.Reinstate) != 0 || len(plan.Roles) != 0 || len(plan.Teams) != 0 {
		t.Fatalf("aborted plan still carries changes: %+v", plan)
	}

	// Exactly the floor (five of a large set) is allowed.
	five := users[:5]
	for i := 6; i <= 100; i++ {
		five = append(five, linked(i, fmt.Sprintf("present-%d", i), identity.RoleMember, auth.StatusActive, nil))
	}
	dir := make([]identityldap.DirectoryUser, 0, 95)
	for i := 6; i <= 100; i++ {
		dir = append(dir, identityldap.DirectoryUser{ExternalID: fmt.Sprintf("present-%d", i)})
	}
	if plan := planDirectoryReconcile(dir, five, nil, nil, identity.RoleMember); plan.Aborted || len(plan.Suspend) != 5 {
		t.Fatalf("plan at the floor = %+v, want five suspensions", plan)
	}
}

// TestReconcile_RoleResync: the mapping is applied to the stored role and
// teams.
func TestReconcile_RoleResync(t *testing.T) {
	dir := []identityldap.DirectoryUser{{ExternalID: "ext-1", Groups: []string{"baleia-admins", "team-eng"}}}
	users := []reconcilableUser{linked(1, "ext-1", identity.RoleMember, auth.StatusActive, nil)}
	roles := []identity.RoleMapping{{Group: "baleia-admins", Role: identity.RoleAdmin}}
	teams := []identity.TeamMapping{{Group: "team-eng", TeamID: "team-1", TeamRole: identity.TeamRoleOwner}}

	plan := planDirectoryReconcile(dir, users, roles, teams, identity.RoleMember)
	if plan.Aborted {
		t.Fatal("plan aborted")
	}
	if len(plan.Roles) != 1 || plan.Roles[0].Role != identity.RoleAdmin {
		t.Fatalf("roles = %+v, want admin", plan.Roles)
	}
	if len(plan.Teams) != 1 || plan.Teams[0].TeamID != "team-1" {
		t.Fatalf("teams = %+v", plan.Teams)
	}
}
