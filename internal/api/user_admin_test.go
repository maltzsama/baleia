package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func userID(t *testing.T, raw string) openapi_types.UUID {
	t.Helper()
	return openapi_types.UUID(mustUUID(t, raw).Bytes)
}

// TestSuspend_NonAdmin403: suspension is a global-admin operation.
func TestSuspend_NonAdmin403(t *testing.T) {
	f := newAuditFixture(t)
	id := userID(t, f.owner)
	got, err := f.h.SuspendUser(ctxAs(f.owner, "member"), SuspendUserRequestObject{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(SuspendUser403JSONResponse); !ok {
		t.Fatalf("suspend = %T, want 403", got)
	}
	if got, err := f.h.ReinstateUser(ctxAs(f.owner, "member"), ReinstateUserRequestObject{Id: id}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(ReinstateUser403JSONResponse); !ok {
		t.Fatalf("reinstate = %T, want 403", got)
	}
}

// TestSuspend_RevokesTokens: suspension ends every session and deletes the
// user's API keys.
func TestSuspend_RevokesTokens(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")

	keyResp, err := f.h.CreateApiKey(ctxAs(f.owner, "member"), CreateApiKeyRequestObject{Body: &CreateApiKeyRequest{Name: "ci"}})
	if err != nil {
		t.Fatal(err)
	}
	key, ok := keyResp.(CreateApiKey201JSONResponse)
	if !ok {
		t.Fatalf("create key = %T", keyResp)
	}

	got, err := f.h.SuspendUser(ctxAs(f.admin, "admin"), SuspendUserRequestObject{Id: userID(t, f.owner)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(SuspendUser200JSONResponse); !ok {
		t.Fatalf("suspend = %T, want 200", got)
	}

	rt, err := f.h.queries.GetRefreshTokenByHashAny(ctx, auth.HashRefreshToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if !rt.Revoked {
		t.Error("refresh token survived the suspension")
	}
	if _, err := f.h.queries.GetAPIKeyByHash(ctx, auth.HashAPIKey(key.Secret)); err == nil {
		t.Error("api key survived the suspension")
	}

	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, mustUUID(t, f.owner)).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != auth.StatusSuspended {
		t.Errorf("status = %q, want suspended", status)
	}
}

// TestSuspend_Audited: the change is one allowed user.suspend event.
func TestSuspend_Audited(t *testing.T) {
	f := newAuditFixture(t)
	if _, err := f.h.SuspendUser(ctxAs(f.admin, "admin"), SuspendUserRequestObject{Id: userID(t, f.owner)}); err != nil {
		t.Fatal(err)
	}
	ev := one(t, "user.suspend", f.mem.take(), audit.UserSuspend, audit.Allowed)
	if ev.Changes["status"].To != auth.StatusSuspended {
		t.Fatalf("changes = %+v", ev.Changes)
	}
}

// TestReinstate_Reactivates: an admin can lift a suspension.
func TestReinstate_Reactivates(t *testing.T) {
	f := newAuditFixture(t)
	ctx := ctxAs(f.admin, "admin")
	id := userID(t, f.owner)
	if _, err := f.h.SuspendUser(ctx, SuspendUserRequestObject{Id: id}); err != nil {
		t.Fatal(err)
	}
	got, err := f.h.ReinstateUser(ctx, ReinstateUserRequestObject{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(ReinstateUser200JSONResponse); !ok {
		t.Fatalf("reinstate = %T, want 200", got)
	}
	var status string
	if err := f.pool.QueryRow(context.Background(), `SELECT status FROM users WHERE id = $1`, mustUUID(t, f.owner)).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != auth.StatusActive {
		t.Errorf("status = %q, want active", status)
	}
}

// TestSuspendedCannotLogin: a suspended local account cannot authenticate even
// with the right password.
func TestSuspendedCannotLogin(t *testing.T) {
	f := newAuthFixture(t)
	if _, err := f.h.SuspendUser(ctxAs(f.admin, "admin"), SuspendUserRequestObject{Id: userID(t, f.owner)}); err != nil {
		t.Fatal(err)
	}
	resp, err := f.h.Login(context.Background(), LoginRequestObject{
		Body: &LoginRequest{Email: emailPtr("owner@x.io"), Password: "correct horse"},
	})
	if err != nil {
		t.Fatal(err)
	}
	denied, ok := resp.(Login403JSONResponse)
	if !ok {
		t.Fatalf("login = %T, want 403", resp)
	}
	if denied.Code != "account_suspended" {
		t.Errorf("code = %q", denied.Code)
	}
}

// TestSuspendedCannotRefresh: a token that survived (or was minted after) the
// suspension is refused by the status gate.
func TestSuspendedCannotRefresh(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	if _, err := f.h.SuspendUser(ctxAs(f.admin, "admin"), SuspendUserRequestObject{Id: userID(t, f.owner)}); err != nil {
		t.Fatal(err)
	}
	user, err := f.h.queries.GetUserByEmail(ctx, "owner@x.io")
	if err != nil {
		t.Fatal(err)
	}
	raw := "surviving-refresh-token"
	if _, err := f.h.queries.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID: user.ID, TokenHash: auth.HashRefreshToken(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}, FamilyID: newTokenFamily(),
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := f.h.RefreshToken(ctx, RefreshTokenRequestObject{Body: &RefreshRequest{RefreshToken: &raw}})
	if err != nil {
		t.Fatal(err)
	}
	denied, ok := resp.(RefreshToken401JSONResponse)
	if !ok {
		t.Fatalf("refresh = %T, want 401", resp)
	}
	if denied.Code != "account_suspended" {
		t.Errorf("code = %q", denied.Code)
	}
}

// TestSuspendedCannotOIDC: the callback lands on /login?error=account_suspended.
func TestSuspendedCannotOIDC(t *testing.T) {
	f := newOIDCFlow(t)
	state, cookie := f.start(t)
	if result := f.finish(t, state, cookie); result.Location != "/sql" {
		t.Fatalf("first login: %q", result.Location)
	}
	user, err := f.q.GetUserByExternalID(context.Background(), db.GetUserByExternalIDParams{
		AuthProviderID: f.provider.ID, ExternalID: &f.idp.Subject,
	})
	if err != nil {
		t.Fatal(err)
	}
	reason := "admin"
	if _, err := f.q.SuspendUser(context.Background(), db.SuspendUserParams{ID: user.ID, SuspendedReason: &reason}); err != nil {
		t.Fatal(err)
	}

	state, cookie = f.start(t)
	result := f.finish(t, state, cookie)
	if got := f.failure(t, result); got != oidcErrorAccountSuspended {
		t.Fatalf("error = %q, want %q", got, oidcErrorAccountSuspended)
	}
}

// TestSuspendedCannotLDAP: a suspended directory account gets the uniform
// invalid_credentials answer, never a hint about the suspension.
func TestSuspendedCannotLDAP(t *testing.T) {
	f, ap := ldapLoginFixture(t)
	reason := "admin"
	var userID pgtype.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE external_id = 'stable-1'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.queries.SuspendUser(context.Background(), db.SuspendUserParams{ID: userID, SuspendedReason: &reason}); err != nil {
		t.Fatal(err)
	}

	resp, err := f.h.Login(context.Background(), LoginRequestObject{Body: &LoginRequest{
		ProviderId: providerID(ap), Username: ptr("alice"), Password: "secret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(Login401JSONResponse); !ok {
		t.Fatalf("ldap login = %T, want 401", resp)
	}
}
