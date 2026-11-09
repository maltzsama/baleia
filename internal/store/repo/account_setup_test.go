package repo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/account"
	"github.com/galileostudio/baleia/internal/identity/setup"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	_ account.Store = (*Users)(nil)
	_ setup.Store   = (*Repo)(nil)
)

func openTestGorm(t *testing.T) *gorm.DB {
	t.Helper()
	gdb := testdb.New(t)
	return gdb
}

// TestAccountStore covers the signed-in account's persistence (#774): the
// profile update, the password update and the personal workspace lookup.
func TestAccountStore(t *testing.T) {
	t.Parallel()
	gdb := openTestGorm(t)
	ctx := context.Background()
	r := New(gdb)

	hash := "old-hash"
	user, err := r.CreateUser(ctx, db.CreateUserParams{
		Name: "Ada", Email: "ada@example.test", PasswordHash: &hash, Role: "member", AuthProvider: "local",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	locale := "pt-BR"
	updated, err := r.UpdateUser(ctx, db.UpdateUserParams{
		ID: user.ID, Name: "Ada Lovelace", Email: "ada@example.test", Locale: &locale,
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.Name != "Ada Lovelace" || updated.Locale == nil || *updated.Locale != locale {
		t.Fatalf("UpdateUser = %+v", updated)
	}

	newHash := "new-hash"
	if err := r.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{ID: user.ID, PasswordHash: &newHash}); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	reloaded, err := r.GetUserByID(ctx, user.ID)
	if err != nil || reloaded.PasswordHash == nil || *reloaded.PasswordHash != newHash {
		t.Fatalf("reloaded = %+v, %v", reloaded, err)
	}

	if _, err := r.PersonalWorkspaceID(ctx, user.ID); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("PersonalWorkspaceID(none) = %v, want ErrNoRows", err)
	}
	owner := model.UUID(UUIDString(user.ID))
	ws := model.Workspace{
		ID: model.UUID(NewUUID()), Name: "Ada", Type: "personal",
		PolarisCatalogName: model.Text("ada_" + NewUUID()[:8]), OwnerUserID: &owner,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := gdb.Create(&ws).Error; err != nil {
		t.Fatalf("create personal workspace: %v", err)
	}
	got, err := r.PersonalWorkspaceID(ctx, user.ID)
	if err != nil || got != PGUUID(string(ws.ID)) {
		t.Fatalf("PersonalWorkspaceID = %v, %v; want %v", got, err, ws.ID)
	}
}

// TestWorkspaceMembers covers the GORM membership read and delete (#653,
// #774): the effective role of a shared workspace's team members, an
// explicit grant and its removal.
func TestWorkspaceMembers(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()

	rows, err := f.r.ListWorkspaceMembers(ctx, f.wsID())
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListWorkspaceMembers = %+v, %v", rows, err)
	}
	byID := make(map[string]db.ListWorkspaceMembersRow, len(rows))
	for _, row := range rows {
		byID[UUIDString(row.User.ID)] = row
	}
	owner := byID[UUIDString(f.ownerID())]
	member := byID[UUIDString(f.memberID())]
	if owner.Role != "admin" || owner.Source != "team" || owner.RoleSet {
		t.Fatalf("owner row = %+v", owner)
	}
	if member.Role != "editor" || member.Source != "team" || member.RoleSet {
		t.Fatalf("member row = %+v", member)
	}

	if n, err := f.r.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{
		WorkspaceID: f.wsID(), UserID: f.memberID(), Role: "viewer",
	}); err != nil || n != 1 {
		t.Fatalf("UpsertWorkspaceMember = %d, %v", n, err)
	}
	rows, err = f.r.ListWorkspaceMembers(ctx, f.wsID())
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListWorkspaceMembers(after grant) = %+v, %v", rows, err)
	}
	for _, row := range rows {
		if UUIDString(row.User.ID) == UUIDString(f.memberID()) {
			if row.Role != "viewer" || !row.RoleSet {
				t.Fatalf("granted member = %+v", row)
			}
		}
	}

	if n, err := f.r.DeleteWorkspaceMember(ctx, db.DeleteWorkspaceMemberParams{
		WorkspaceID: f.wsID(), UserID: f.memberID(),
	}); err != nil || n != 1 {
		t.Fatalf("DeleteWorkspaceMember = %d, %v", n, err)
	}
	rows, err = f.r.ListWorkspaceMembers(ctx, f.wsID())
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListWorkspaceMembers(after delete) = %+v, %v", rows, err)
	}
	for _, row := range rows {
		if UUIDString(row.User.ID) == UUIDString(f.memberID()) && (row.Role != "editor" || row.RoleSet) {
			t.Fatalf("member after delete = %+v", row)
		}
	}

	// A stale grant does not keep a former team member in a shared workspace.
	if n, err := f.r.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{
		WorkspaceID: f.wsID(), UserID: f.memberID(), Role: "viewer",
	}); err != nil || n != 1 {
		t.Fatalf("UpsertWorkspaceMember(stale) = %d, %v", n, err)
	}
	if err := f.gdb.Where("team_id = ? AND user_id = ?", string(f.teamID(t)), UUIDString(f.memberID())).
		Delete(&model.TeamMember{}).Error; err != nil {
		t.Fatalf("remove team member: %v", err)
	}
	rows, err = f.r.ListWorkspaceMembers(ctx, f.wsID())
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListWorkspaceMembers(former team member) = %+v, %v", rows, err)
	}
	if UUIDString(rows[0].User.ID) != UUIDString(f.ownerID()) {
		t.Fatalf("former team member with a stale grant is still listed: %+v", rows)
	}
}

// TestFirstAdminSetupRace: two concurrent setups of a fresh installation make
// exactly one administrator, without an advisory lock (#375, ADR 0020).
func TestFirstAdminSetupRace(t *testing.T) {
	t.Parallel()
	gdb := openTestGorm(t)
	ctx := context.Background()
	r := New(gdb)
	svc := setup.New(r, r.TxSetup, bcrypt.MinCost)

	configured, err := svc.Configured(ctx)
	if err != nil || configured {
		t.Fatalf("Configured(fresh) = %v, %v", configured, err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = svc.CreateFirstAdmin(ctx,
				fmt.Sprintf("Admin %d", i), fmt.Sprintf("admin%d@example.test", i), "password123")
		}(i)
	}
	close(start)
	wg.Wait()

	var created, already int
	for _, err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, setup.ErrAlreadySetUp):
			already++
		default:
			t.Fatalf("CreateFirstAdmin: %v", err)
		}
	}
	if created != 1 || already != 1 {
		t.Fatalf("created = %d, already set up = %d; want one of each", created, already)
	}

	var admins int64
	if err := gdb.Model(&model.User{}).Where("role = ?", "admin").Count(&admins).Error; err != nil || admins != 1 {
		t.Fatalf("admin count = %d, %v", admins, err)
	}
	configured, err = svc.Configured(ctx)
	if err != nil || !configured {
		t.Fatalf("Configured(after) = %v, %v", configured, err)
	}
	var locks int64
	if err := gdb.Model(&model.SetupLock{}).Count(&locks).Error; err != nil || locks != 1 {
		t.Fatalf("setup lock rows = %d, %v", locks, err)
	}
}
