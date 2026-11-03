package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestRequireWorkspaceRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	newID := func() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }
	ws, viewer, editor, admin, outsider, agent := newID(), newID(), newID(), newID(), newID(), newID()
	st := store{
		wss: map[[16]byte]db.Workspace{ws.Bytes: {ID: ws}},
		wsRoles: map[[2][16]byte]string{
			{ws.Bytes, viewer.Bytes}: "viewer",
			{ws.Bytes, editor.Bytes}: "editor",
			{ws.Bytes, admin.Bytes}:  "admin",
		},
		agentBound: map[[2][16]byte]bool{{agent.Bytes, ws.Bytes}: true},
	}
	c := New(st)
	user := func(id pgtype.UUID) Principal { return Principal{UserID: id, Role: "member"} }
	cases := []struct {
		name string
		p    Principal
		min  WorkspaceRole
		want error
	}{
		{"viewer may view", user(viewer), WorkspaceViewer, nil},
		{"viewer may not edit", user(viewer), WorkspaceEditor, ErrForbidden},
		{"editor may edit", user(editor), WorkspaceEditor, nil},
		{"editor may not administer", user(editor), WorkspaceAdmin, ErrForbidden},
		{"admin may administer", user(admin), WorkspaceAdmin, nil},
		{"global admin administers every workspace", Principal{UserID: outsider, Role: RoleAdmin}, WorkspaceAdmin, nil},
		{"a non-member does not see it", user(outsider), WorkspaceViewer, ErrNotFound},
		{"an agent acts with its owner's role", Principal{UserID: editor, Role: "member", Kind: KindAgent, AgentID: agent}, WorkspaceEditor, nil},
		{"an agent never exceeds its owner's role", Principal{UserID: viewer, Role: "member", Kind: KindAgent, AgentID: agent}, WorkspaceEditor, ErrForbidden},
		{"an unbound agent does not see it", Principal{UserID: admin, Role: "member", Kind: KindAgent, AgentID: newID()}, WorkspaceViewer, ErrNotFound},
	}
	for _, tt := range cases {
		if _, err := c.RequireWorkspaceRole(ctx, tt.p, ws, tt.min); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
	if _, err := c.RequireWorkspaceRole(ctx, Principal{UserID: outsider, Role: RoleAdmin}, newID(), WorkspaceViewer); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown workspace: %v", err)
	}
}

func TestParseWorkspaceRole(t *testing.T) {
	t.Parallel()
	for _, r := range []WorkspaceRole{WorkspaceViewer, WorkspaceEditor, WorkspaceAdmin} {
		if ParseWorkspaceRole(r.String()) != r {
			t.Errorf("round trip %v", r)
		}
	}
	if ParseWorkspaceRole("owner") != WorkspaceNone {
		t.Error("unknown roles are none")
	}
}
