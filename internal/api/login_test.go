package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func emailPtr(s string) *openapi_types.Email {
	e := openapi_types.Email(s)
	return &e
}

// fakeLoginQuerier answers GetUserByEmail and nothing else.
type fakeLoginQuerier struct {
	db.Querier
	user db.User
	err  error
}

func (f *fakeLoginQuerier) GetUserByEmail(context.Context, string) (db.User, error) {
	return f.user, f.err
}

func loginHandlers(q db.Querier) *Handlers {
	return &Handlers{queries: q, tx: directTx{q: q}, cfg: &config.Config{BcryptCost: testBcryptCost}}
}

func loginResponse(t *testing.T, resp LoginResponseObject, err error, name string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: err = %v", name, err)
	}
	unauthorized, ok := resp.(Login401JSONResponse)
	if !ok {
		t.Fatalf("%s: %T, want 401", name, resp)
	}
	if unauthorized.Code != "invalid_credentials" || unauthorized.Message != "invalid credentials" {
		t.Errorf("%s: %s %q, want invalid_credentials", name, unauthorized.Code, unauthorized.Message)
	}
}

func TestLogin_EmailRequiredForLocal(t *testing.T) {
	resp, err := loginHandlers(&fakeLoginQuerier{}).Login(context.Background(), LoginRequestObject{
		Body: &LoginRequest{Password: "whatever"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(Login422JSONResponse); !ok {
		t.Fatalf("login = %T, want 422", resp)
	}
}

// TestLogin_UniformErrors: an unknown e-mail, an SSO-only account and a wrong
// password are indistinguishable (#51).
func TestLogin_UniformErrors(t *testing.T) {
	wrongHash, err := auth.HashPasswordWithCost("right", testBcryptCost)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		handle *Handlers
	}{
		{"unknown e-mail", loginHandlers(&fakeLoginQuerier{err: errors.New("no rows")})},
		{"sso-only account", loginHandlers(&fakeLoginQuerier{user: db.User{AuthProvider: "oidc"}})},
		{"wrong password", loginHandlers(&fakeLoginQuerier{user: db.User{AuthProvider: "local", PasswordHash: &wrongHash}})},
	}
	for _, tc := range cases {
		resp, err := tc.handle.Login(context.Background(), LoginRequestObject{
			Body: &LoginRequest{Email: emailPtr("user@example.test"), Password: "wrong"},
		})
		loginResponse(t, resp, err, tc.name)
	}
}

// TestLogin_TimingEqualized: the unknown-e-mail path runs bcrypt instead of
// returning early (#51).
func TestLogin_TimingEqualized(t *testing.T) {
	const minBcryptTime = 15 * time.Millisecond

	start := time.Now()
	resp, err := loginHandlers(&fakeLoginQuerier{err: errors.New("no rows")}).Login(
		context.Background(),
		LoginRequestObject{Body: &LoginRequest{Email: emailPtr("nobody@example.test"), Password: "whatever"}},
	)
	unknown := time.Since(start)
	loginResponse(t, resp, err, "unknown e-mail")
	if unknown < minBcryptTime {
		t.Errorf("unknown e-mail answered in %s; bcrypt was probably skipped", unknown)
	}

	hash, err := auth.HashPasswordWithCost("right", testBcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	resp, err = loginHandlers(&fakeLoginQuerier{user: db.User{AuthProvider: "local", PasswordHash: &hash}}).Login(
		context.Background(),
		LoginRequestObject{Body: &LoginRequest{Email: emailPtr("user@example.test"), Password: "wrong"}},
	)
	wrong := time.Since(start)
	loginResponse(t, resp, err, "wrong password")
	if wrong < minBcryptTime {
		t.Errorf("wrong password answered in %s; bcrypt was probably skipped", wrong)
	}
}
