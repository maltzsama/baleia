package authz

import (
	"context"
	"errors"
	"testing"
)

func TestSpaceRoleString(t *testing.T) {
	for _, tt := range []struct {
		role SpaceRole
		want string
	}{
		{SpaceNone, ""},
		{SpaceViewer, "viewer"},
		{SpaceEditor, "editor"},
		{SpaceManager, "manager"},
	} {
		if got := tt.role.String(); got != tt.want {
			t.Errorf("SpaceRole(%d).String() = %q, want %q", tt.role, got, tt.want)
		}
	}
}

func TestEffectiveSpaceRoleMapping(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want SpaceRole
	}{
		{"", SpaceNone},
		{"viewer", SpaceViewer},
		{"editor", SpaceEditor},
		{"manager", SpaceManager},
		{"nonsense", SpaceNone},
	} {
		if got := spaceRoleFromString(tt.in); got != tt.want {
			t.Errorf("spaceRoleFromString(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestRequireSpace(t *testing.T) {
	ws, sp := uid(30), uid(31)
	p := Principal{UserID: uid(4), Role: "member"}
	cases := []struct {
		name    string
		role    string
		min     SpaceRole
		wantErr error
		want    SpaceRole
	}{
		{"invisible", "", SpaceViewer, ErrNotFound, SpaceNone},
		{"viewer_below_editor", "viewer", SpaceEditor, ErrForbidden, SpaceViewer},
		{"editor", "editor", SpaceEditor, nil, SpaceEditor},
		{"manager_above_editor", "manager", SpaceEditor, nil, SpaceManager},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := New(store{spaceRoles: map[[3][16]byte]string{
				{ws.Bytes, sp.Bytes, p.UserID.Bytes}: tt.role,
			}})
			got, err := c.RequireSpace(context.Background(), p, ws, sp, tt.min)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RequireSpace() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("RequireSpace() role = %v, want %v", got, tt.want)
			}
		})
	}
}
