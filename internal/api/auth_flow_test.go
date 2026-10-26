package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/mail"
	"github.com/galileostudio/baleia/internal/testdb"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// testBcryptCost keeps password tests cheap; it is the configured minimum.
const testBcryptCost = 10

func newAuthFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := newAuditFixture(t)
	f.h.settings.Auth.PasswordCost = testBcryptCost
	return f
}

func loginRefresh(t *testing.T, h *API, email, password string) string {
	t.Helper()
	resp, err := h.Login(context.Background(), LoginRequestObject{
		Body: &LoginRequest{Email: emailPtr(email), Password: password},
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(Login200JSONResponse)
	if !found || ok.RefreshToken == nil {
		t.Fatalf("login = %T, want 200 with a refresh token", resp)
	}
	return *ok.RefreshToken
}

func refreshStatus(t *testing.T, h *API, token string) int {
	t.Helper()
	resp, err := h.RefreshToken(context.Background(), RefreshTokenRequestObject{
		Body: &RefreshRequest{RefreshToken: &token},
	})
	if err != nil {
		t.Fatal(err)
	}
	switch resp.(type) {
	case RefreshToken200JSONResponse:
		return 200
	case RefreshToken401JSONResponse:
		return 401
	default:
		t.Fatalf("refresh = %T", resp)
		return 0
	}
}

func refreshToken(t *testing.T, h *API, token string) string {
	t.Helper()
	resp, err := h.RefreshToken(context.Background(), RefreshTokenRequestObject{
		Body: &RefreshRequest{RefreshToken: &token},
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(RefreshToken200JSONResponse)
	if !found || ok.RefreshToken == nil {
		t.Fatalf("refresh = %T, want 200 with a refresh token", resp)
	}
	return *ok.RefreshToken
}

// TestGetMe_IncludesStatus: the profile endpoint honours the required
// active/suspended contract (#266 follow-up).
func TestGetMe_IncludesStatus(t *testing.T) {
	pool := testdb.New(t)
	q := db.New(pool)
	ctx := context.Background()
	hash, err := auth.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Name: "Me", Email: "me@x.io", PasswordHash: &hash, Role: "admin", AuthProvider: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := testAPI(handlerFields{queries: q, pool: pool, tx: poolTx{pool: pool}})

	resp, err := h.GetMe(ctxAs(user.ID.String(), "admin"), GetMeRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	me, ok := resp.(GetMe200JSONResponse)
	if !ok || me.Status != MeStatus(auth.StatusActive) {
		t.Fatalf("me = %#v, want active", resp)
	}

	reason := "admin"
	if _, err := q.SuspendUser(ctx, db.SuspendUserParams{ID: user.ID, SuspendedReason: &reason}); err != nil {
		t.Fatal(err)
	}
	resp, err = h.GetMe(ctxAs(user.ID.String(), "admin"), GetMeRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if me, ok := resp.(GetMe200JSONResponse); !ok || me.Status != MeStatus(auth.StatusSuspended) {
		t.Fatalf("me = %#v, want suspended", resp)
	}
}

func TestRefresh_ConcurrentSingleWinner(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")

	start := make(chan struct{})
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			resp, err := f.h.RefreshToken(context.Background(), RefreshTokenRequestObject{
				Body: &RefreshRequest{RefreshToken: &token},
			})
			if err != nil {
				results <- -1
				return
			}
			switch resp.(type) {
			case RefreshToken200JSONResponse:
				results <- 200
			case RefreshToken401JSONResponse:
				results <- 401
			default:
				results <- 0
			}
		}()
	}
	close(start)

	statuses := map[int]int{}
	for i := 0; i < 2; i++ {
		statuses[<-results]++
	}
	if statuses[200] != 1 || statuses[401] != 1 {
		t.Fatalf("statuses = %v, want exactly one 200 and one 401", statuses)
	}
}

func TestRefresh_FamilyPreserved(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")
	rotated := refreshToken(t, f.h, token)

	ctx := context.Background()
	before, err := f.h.queries.GetRefreshTokenByHashAny(ctx, auth.HashRefreshToken(token))
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.h.queries.GetRefreshTokenByHashAny(ctx, auth.HashRefreshToken(rotated))
	if err != nil {
		t.Fatal(err)
	}
	if !before.FamilyID.Valid || before.FamilyID != after.FamilyID {
		t.Errorf("family = %v, rotated token family = %v, want the same non-null family", before.FamilyID, after.FamilyID)
	}
}

func TestRefresh_ReuseRevokesFamily(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")
	rotated := refreshToken(t, f.h, token)
	f.mem.take()

	if got := refreshStatus(t, f.h, token); got != 401 {
		t.Fatalf("replayed token = %d, want 401", got)
	}

	var sawReuse bool
	for _, e := range f.mem.take() {
		if e.Action == audit.AuthRefresh && e.Outcome == audit.Denied && e.Reason == audit.ReasonReuseDetected {
			sawReuse = true
		}
	}
	if !sawReuse {
		t.Error("reuse was not audited as auth.refresh/reuse_detected")
	}

	// The whole family is gone, so the rotated token no longer refreshes.
	if got := refreshStatus(t, f.h, rotated); got != 401 {
		t.Errorf("rotated token after reuse = %d, want 401", got)
	}
}

// TestRefresh_CookieFallback: a browser refreshes with only the HttpOnly
// cookie, no body token (#51).
func TestRefresh_CookieFallback(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")

	ctx := auth.WithRefreshCookie(context.Background(), token)
	resp, err := f.h.RefreshToken(ctx, RefreshTokenRequestObject{Body: &RefreshRequest{}})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(RefreshToken200JSONResponse)
	if !found || ok.RefreshToken == nil {
		t.Fatalf("cookie refresh = %T, want 200 with a rotated token", resp)
	}
}

// TestLogout_CookieRevokesFamily: logout revokes the family behind the cookie
// token, so neither it nor the rotated token works afterwards (#51).
func TestLogout_CookieRevokesFamily(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")
	rotated := refreshToken(t, f.h, token)

	ctx := auth.WithRefreshCookie(context.Background(), rotated)
	resp, err := f.h.Logout(ctx, LogoutRequestObject{Body: &LogoutRequest{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(Logout204Response); !ok {
		t.Fatalf("logout = %T, want 204", resp)
	}
	if got := refreshStatus(t, f.h, rotated); got != 401 {
		t.Errorf("refresh after logout = %d, want 401", got)
	}
}

// TestCreateFirstAdmin_RejectsShortPassword: the API enforces the documented
// 8-character minimum even though oapi-codegen does not validate bodies.
func TestCreateFirstAdmin_RejectsShortPassword(t *testing.T) {
	pool := testdb.New(t)
	h := testAPI(handlerFields{queries: db.New(pool), settings: SettingsFromConfig(&config.Config{BcryptCost: testBcryptCost})})
	resp, err := h.CreateFirstAdmin(context.Background(), CreateFirstAdminRequestObject{
		Body: &CreateAdminRequest{Name: "Admin", Email: "admin@example.test", Password: "short"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(CreateFirstAdmin422JSONResponse); !ok {
		t.Fatalf("short password = %T, want 422", resp)
	}
}

func TestUpdatePassword_RejectsShortPassword(t *testing.T) {
	f := newAuthFixture(t)
	resp, err := f.h.UpdatePassword(ctxAs(f.owner, "member"), UpdatePasswordRequestObject{
		Body: &UpdatePasswordRequest{CurrentPassword: "correct horse", NewPassword: "short"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(UpdatePassword422JSONResponse); !ok {
		t.Fatalf("short password = %T, want 422", resp)
	}
}

// TestReset_ShortPasswordDoesNotConsumeToken: the length check runs before the
// single-use token is consumed.
func TestReset_ShortPasswordDoesNotConsumeToken(t *testing.T) {
	f := newAuthFixture(t)
	const raw = "short-password-reset"
	createResetToken(t, f, f.owner, raw, time.Now().Add(time.Hour))

	resp, err := f.h.ConfirmPasswordReset(context.Background(), ConfirmPasswordResetRequestObject{
		Body: &PasswordResetConfirmBody{Token: raw, NewPassword: "short"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ConfirmPasswordReset422JSONResponse); !ok {
		t.Fatalf("short password = %T, want 422", resp)
	}

	resp, err = f.h.ConfirmPasswordReset(context.Background(), ConfirmPasswordResetRequestObject{
		Body: &PasswordResetConfirmBody{Token: raw, NewPassword: "long-enough"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ConfirmPasswordReset204Response); !ok {
		t.Fatalf("the short password consumed the reset token: %T", resp)
	}
}

func createResetToken(t *testing.T, f *auditFixture, userID string, raw string, expires time.Time) {
	t.Helper()
	if err := f.h.queries.CreatePasswordResetToken(context.Background(), db.CreatePasswordResetTokenParams{
		TokenHash: auth.HashToken(raw),
		UserID:    mustUUID(t, userID),
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReset_HashedSingleUse(t *testing.T) {
	f := newAuthFixture(t)
	const raw = "raw-reset-token"
	createResetToken(t, f, f.owner, raw, time.Now().Add(time.Hour))

	// The database holds the hash, never the raw link token.
	var stored string
	if err := f.pool.QueryRow(context.Background(),
		"SELECT token_hash FROM password_reset_tokens WHERE token_hash = $1", auth.HashToken(raw),
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != auth.HashToken(raw) || stored == raw {
		t.Fatalf("stored token = %q, want its hash only", stored)
	}

	resp, err := f.h.ConfirmPasswordReset(context.Background(), ConfirmPasswordResetRequestObject{
		Body: &PasswordResetConfirmBody{Token: raw, NewPassword: "a-new-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ConfirmPasswordReset204Response); !ok {
		t.Fatalf("first use = %T, want 204", resp)
	}

	resp, err = f.h.ConfirmPasswordReset(context.Background(), ConfirmPasswordResetRequestObject{
		Body: &PasswordResetConfirmBody{Token: raw, NewPassword: "another-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ConfirmPasswordReset401JSONResponse); !ok {
		t.Fatalf("second use = %T, want 401", resp)
	}
}

func TestReset_Expired(t *testing.T) {
	f := newAuthFixture(t)
	const raw = "expired-reset-token"
	createResetToken(t, f, f.owner, raw, time.Now().Add(-time.Minute))

	resp, err := f.h.ConfirmPasswordReset(context.Background(), ConfirmPasswordResetRequestObject{
		Body: &PasswordResetConfirmBody{Token: raw, NewPassword: "a-new-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ConfirmPasswordReset401JSONResponse); !ok {
		t.Fatalf("expired token = %T, want 401", resp)
	}
}

func TestReset_RevokesRefresh(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")
	const raw = "reset-revokes-refresh"
	createResetToken(t, f, f.owner, raw, time.Now().Add(time.Hour))

	resp, err := f.h.ConfirmPasswordReset(context.Background(), ConfirmPasswordResetRequestObject{
		Body: &PasswordResetConfirmBody{Token: raw, NewPassword: "a-new-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ConfirmPasswordReset204Response); !ok {
		t.Fatalf("reset = %T, want 204", resp)
	}
	if got := refreshStatus(t, f.h, token); got != 401 {
		t.Errorf("refresh after reset = %d, want 401", got)
	}
}

func TestReset_RequestStoresHash(t *testing.T) {
	f := newAuthFixture(t)
	f.h.settings.Mail.SMTPHost, f.h.settings.Mail.SMTPPort = "smtp.example.test", "25"
	var sentURL string
	f.h.mailer = mailerFunc(func(_ context.Context, _, locale string, template mail.Template, data map[string]any) error {
		if template != mail.TemplatePasswordReset {
			t.Fatalf("template = %q, want %q", template, mail.TemplatePasswordReset)
		}
		if locale != "en" {
			t.Errorf("locale = %q, want en (the fixture has no preference)", locale)
		}
		sentURL, _ = data["ResetURL"].(string)
		return nil
	})

	resp, err := f.h.RequestPasswordReset(context.Background(), RequestPasswordResetRequestObject{
		Body: &PasswordResetRequestBody{Email: "owner@x.io"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(RequestPasswordReset204Response); !ok {
		t.Fatalf("request = %T, want 204", resp)
	}

	u, err := url.Parse(sentURL)
	if err != nil {
		t.Fatal(err)
	}
	raw := u.Query().Get("token")
	if raw == "" {
		t.Fatal("the e-mail link carried no token")
	}
	var n int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM password_reset_tokens WHERE token_hash = $1", auth.HashToken(raw),
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("stored rows for the hash = %d, want 1", n)
	}
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM password_reset_tokens WHERE token_hash = $1", raw,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the raw token was stored")
	}
}

func TestPasswordChange_RevokesRefresh(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")

	resp, err := f.h.UpdatePassword(ctxAs(f.owner, "member"), UpdatePasswordRequestObject{
		Body: &UpdatePasswordRequest{CurrentPassword: "correct horse", NewPassword: "a-new-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(UpdatePassword204Response); !ok {
		t.Fatalf("password change = %T, want 204", resp)
	}
	if got := refreshStatus(t, f.h, token); got != 401 {
		t.Errorf("refresh after password change = %d, want 401", got)
	}
}

func TestRoleChange_RevokesRefresh(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")
	ownerID := openapi_types.UUID(mustUUID(t, f.owner).Bytes)

	resp, err := f.h.UpdateUserRole(ctxAs(f.admin, "admin"), UpdateUserRoleRequestObject{
		Id: ownerID, Body: &UpdateUserRoleRequest{Role: "admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(UpdateUserRole200JSONResponse); !ok {
		t.Fatalf("role change = %T, want 200", resp)
	}
	if got := refreshStatus(t, f.h, token); got != 401 {
		t.Errorf("refresh after role change = %d, want 401", got)
	}
}

// failingRevoke is the real querier with RevokeRefreshFamily failing.
type failingRevoke struct{ db.Querier }

func (failingRevoke) RevokeRefreshFamily(context.Context, pgtype.UUID) error {
	return errors.New("db down")
}

// TestLogout_RevokeFailureIsAnError: a failed revoke is not reported as a
// successful logout, and the audit trail records the failure (#370).
func TestLogout_RevokeFailureIsAnError(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")
	f.mem.take()
	f.h.queries = failingRevoke{f.h.queries}
	f.h.access = wsaccess.New(f.h.queries)
	f.h.topology = topology.New(f.h.queries)
	f.h.sessions = newSessions(f.h.core)

	if _, err := f.h.Logout(context.Background(), LogoutRequestObject{Body: &LogoutRequest{RefreshToken: &token}}); err == nil {
		t.Fatal("logout with a failed revoke returned no error")
	}
	events := f.mem.take()
	if len(events) != 1 || events[0].Action != audit.AuthLogout || events[0].Outcome != audit.Error {
		t.Fatalf("audit = %+v, want one failed auth.logout", events)
	}
}

// TestRefresh_ReuseRevokeFailureIsAnError: a replayed token whose family
// cannot be revoked is a server error, not a quiet 401 (#370).
func TestRefresh_ReuseRevokeFailureIsAnError(t *testing.T) {
	f := newAuthFixture(t)
	token := loginRefresh(t, f.h, "owner@x.io", "correct horse")
	refreshToken(t, f.h, token)
	f.mem.take()
	f.h.queries = failingRevoke{f.h.queries}
	f.h.access = wsaccess.New(f.h.queries)
	f.h.topology = topology.New(f.h.queries)
	f.h.sessions = newSessions(f.h.core)

	if _, err := f.h.RefreshToken(context.Background(), RefreshTokenRequestObject{Body: &RefreshRequest{RefreshToken: &token}}); err == nil {
		t.Fatal("replay with a failed revoke returned no error")
	}
	events := f.mem.take()
	if len(events) != 1 || events[0].Outcome != audit.Error || events[0].Reason != audit.ReasonReuseDetected {
		t.Fatalf("audit = %+v, want one failed reuse_detected", events)
	}
}

// TestCreateFirstAdmin_ConcurrentSetupMakesOneAdmin: concurrent setups with
// different e-mails create exactly one admin; the others get 409 (#375).
func TestCreateFirstAdmin_ConcurrentSetupMakesOneAdmin(t *testing.T) {
	pool := testdb.New(t)
	h := testAPI(handlerFields{queries: db.New(pool), pool: pool, tx: poolTx{pool: pool}, settings: SettingsFromConfig(&config.Config{BcryptCost: testBcryptCost})})
	const n = 8
	var wg sync.WaitGroup
	created := make(chan bool, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := h.CreateFirstAdmin(context.Background(), CreateFirstAdminRequestObject{
				Body: &CreateAdminRequest{Name: "Admin", Email: openapi_types.Email(fmt.Sprintf("admin%d@example.test", i)), Password: "correct horse"},
			})
			if err != nil {
				t.Error(err)
				return
			}
			switch resp.(type) {
			case CreateFirstAdmin201JSONResponse:
				created <- true
			case CreateFirstAdmin409JSONResponse:
			default:
				t.Errorf("setup = %T, want 201 or 409", resp)
			}
		}()
	}
	wg.Wait()
	close(created)
	if len(created) != 1 {
		t.Fatalf("%d setups succeeded, want 1", len(created))
	}
	users, err := db.New(pool).ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("%d users after concurrent setup, want 1", len(users))
	}
}
