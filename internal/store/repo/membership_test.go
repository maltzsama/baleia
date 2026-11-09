package repo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

func TestWorkspaceRole(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)

	ctx := context.Background()
	now := time.Now().UTC()
	store := New(gdb)

	owner := createAPIKeyUser(t, gdb, "ws-owner@example.test")
	member := createAPIKeyUser(t, gdb, "ws-member@example.test")
	outsider := createAPIKeyUser(t, gdb, "ws-outsider@example.test")
	service := model.User{
		ID: model.UUID(NewUUID()), Name: "Service", Email: model.Text("ws-service@example.test"),
		Role: "member", AuthProvider: "service", Status: "active", Kind: "service_account",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&service).Error; err != nil {
		t.Fatalf("create service account: %v", err)
	}

	team := model.Team{
		ID: model.UUID(NewUUID()), Name: "Data", Description: nil,
		CreatedBy: owner.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	for _, tm := range []model.TeamMember{
		{ID: model.UUID(NewUUID()), TeamID: team.ID, UserID: owner.ID, Role: "owner", JoinedAt: now},
		{ID: model.UUID(NewUUID()), TeamID: team.ID, UserID: member.ID, Role: "member", JoinedAt: now},
	} {
		if err := gdb.Create(&tm).Error; err != nil {
			t.Fatalf("create team member: %v", err)
		}
	}

	ownerID, outsiderID, serviceID := owner.ID, outsider.ID, service.ID
	personal := model.Workspace{
		ID: model.UUID(NewUUID()), Name: "Personal", Type: "personal",
		PolarisCatalogName: model.Text("polaris_personal"), OwnerUserID: &ownerID,
		CreatedAt: now, UpdatedAt: now,
	}
	shared := model.Workspace{
		ID: model.UUID(NewUUID()), Name: "Shared", Type: "shared",
		PolarisCatalogName: model.Text("polaris_shared"), OwnerTeamID: &team.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	for _, ws := range []model.Workspace{personal, shared} {
		if err := gdb.Create(&ws).Error; err != nil {
			t.Fatalf("create workspace: %v", err)
		}
	}

	for _, wm := range []model.WorkspaceMember{
		{WorkspaceID: personal.ID, UserID: outsiderID, Role: "editor", GrantedAt: now, Source: "manual"},
		{WorkspaceID: shared.ID, UserID: member.ID, Role: "viewer", GrantedAt: now, Source: "manual"},
		{WorkspaceID: shared.ID, UserID: serviceID, Role: "editor", GrantedAt: now, Source: "manual"},
	} {
		if err := gdb.Create(&wm).Error; err != nil {
			t.Fatalf("create workspace member: %v", err)
		}
	}

	cases := []struct {
		name string
		ws   model.UUID
		user model.UUID
		want string
	}{
		{"personal owner", personal.ID, owner.ID, "admin"},
		{"personal owner uppercase id", personal.ID, model.UUID(strings.ToUpper(string(owner.ID))), "admin"},
		{"personal shared", personal.ID, outsiderID, "editor"},
		{"personal non-member", personal.ID, member.ID, ""},
		{"shared team owner", shared.ID, owner.ID, "admin"},
		{"shared team member overridden", shared.ID, member.ID, "viewer"},
		{"shared outsider", shared.ID, outsiderID, ""},
		{"service account explicit", shared.ID, serviceID, "editor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := store.WorkspaceRole(ctx, string(tc.ws), string(tc.user))
			if err != nil {
				t.Fatalf("WorkspaceRole: %v", err)
			}
			if got != tc.want {
				t.Fatalf("WorkspaceRole = %q, want %q", got, tc.want)
			}
		})
	}
}
