package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func newAuthFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := newAuditFixture(t)
	// Keep bcrypt cheap; the timing-specific test uses its own cost.
	f.h.cfg.BcryptCost = 10
	return f
}

func loginRefresh(t *testing.T, h *Handlers, email, password string) string {
	t.Helper()
	resp, err := h.Login(context.Background(), LoginRequestObject{
		Body: &LoginRequest{Email: openapi_types.Email(email), Password: password},
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(Login200JSONResponse)
	if !found {
		t.Fatalf("login = %T, want 200", resp)
	}
	return ok.RefreshToken
}

func refreshStatus(t *testing.T, h *Handlers, token string) int {
	t.Helper()
	resp, err := h.RefreshToken(context.Background(), RefreshTokenRequestObject{
		Body: &RefreshRequest{RefreshToken: token},
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

func refreshToken(t *testing.T, h *Handlers, token string) string {
	t.Helper()
	resp, err := h.RefreshToken(context.Background(), RefreshTokenRequestObject{
		Body: &RefreshRequest{RefreshToken: token},
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(RefreshToken200JSONResponse)
	if !found {
		t.Fatalf("refresh = %T, want 200", resp)
	}
	return ok.RefreshToken
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
				Body: &RefreshRequest{RefreshToken: token},
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
