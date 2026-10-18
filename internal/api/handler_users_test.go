package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeUserDeleter struct {
	db.Querier
	deleteErr error
	revoked   bool
}

func (f *fakeUserDeleter) GetUserByID(context.Context, pgtype.UUID) (db.User, error) {
	return db.User{}, nil
}

func (f *fakeUserDeleter) DeleteUser(context.Context, pgtype.UUID) error { return f.deleteErr }

func (f *fakeUserDeleter) RevokeUserRefreshTokens(context.Context, pgtype.UUID) error {
	f.revoked = true
	return nil
}

// TestRemoveUserConflict: a user the database still references is refused
// with 409, not 500; the owner of a workspace gets user_owns_workspace.
func TestRemoveUserConflict(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.ContextKeyRole, "admin")
	cases := []struct {
		name     string
		err      error
		wantCode string // "" = 204
	}{
		{"deleted", nil, ""},
		{"owns a workspace", &pgconn.PgError{Code: "23001", ConstraintName: "workspaces_owner_user_id_fkey", TableName: "workspaces"}, "user_owns_workspace"},
		{"other reference", fmt.Errorf("delete: %w", &pgconn.PgError{Code: "23503", ConstraintName: "x_fkey", TableName: "audit"}), "user_in_use"},
	}
	for _, tc := range cases {
		h := &Handlers{queries: &fakeUserDeleter{deleteErr: tc.err}}
		resp, err := h.RemoveUser(ctx, RemoveUserRequestObject{})
		if err != nil {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		switch r := resp.(type) {
		case RemoveUser204Response:
			if tc.wantCode != "" {
				t.Errorf("%s: 204, want %s", tc.name, tc.wantCode)
			}
		case RemoveUser409JSONResponse:
			if r.Code != tc.wantCode {
				t.Errorf("%s: code %q, want %q", tc.name, r.Code, tc.wantCode)
			}
		default:
			t.Errorf("%s: unexpected %T", tc.name, resp)
		}
	}
	// Any other failure is still an error.
	h := &Handlers{queries: &fakeUserDeleter{deleteErr: errors.New("connection reset")}}
	if _, err := h.RemoveUser(ctx, RemoveUserRequestObject{}); err == nil {
		t.Error("a non-FK failure must stay an error")
	}
}

// TestDisable_RevokesRefresh: removing (disabling) a user ends their refresh
// tokens (#51).
func TestDisable_RevokesRefresh(t *testing.T) {
	fake := &fakeUserDeleter{}
	h := &Handlers{queries: fake}
	ctx := context.WithValue(context.Background(), auth.ContextKeyRole, "admin")

	resp, err := h.RemoveUser(ctx, RemoveUserRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(RemoveUser204Response); !ok {
		t.Fatalf("RemoveUser = %T, want 204", resp)
	}
	if !fake.revoked {
		t.Error("RemoveUser did not revoke the user's refresh tokens")
	}
}
