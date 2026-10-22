package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func ldapTestConfig() identityldap.Config {
	return identityldap.Config{
		URL:          "ldaps://ldap.example.test:636",
		BindDN:       "cn=svc,dc=example,dc=test",
		BindPassword: "service-secret",
		BaseDN:       "ou=people,dc=example,dc=test",
		UserFilter:   "(uid={{username}})",
	}.Normalize()
}

// ldapLoginFixture creates a sealed LDAP provider and one linked user.
func ldapLoginFixture(t *testing.T) (*auditFixture, db.AuthProvider) {
	t.Helper()
	f := newAuthFixture(t)
	f.h.loginDelayStore = NewLoginDelay(LoginDelayConfig{
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	ctx := context.Background()

	ap, err := f.h.queries.CreateAuthProvider(ctx, db.CreateAuthProviderParams{
		Name: "corp", Type: "ldap", ConfigEnc: "pending", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	enc, err := identityldap.SealConfig(f.h.keyring, idString(ap.ID), ldapTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	ap, err = f.h.queries.UpdateAuthProvider(ctx, db.UpdateAuthProviderParams{
		ID: ap.ID, Name: ap.Name, ConfigEnc: enc, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var userID pgtype.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO users (name, email, role, auth_provider, auth_provider_id, external_id)
		VALUES ('Alice', 'alice@corp.test', 'member', 'ldap', $1, 'stable-1') RETURNING id`, ap.ID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	f.h.ldapAuthenticate = func(_ context.Context, _ identityldap.Config, username, password string) (*identityldap.User, error) {
		if username != "alice" || password != "secret" {
			return nil, identityldap.ErrInvalidCredentials
		}
		return &identityldap.User{
			ExternalID: "stable-1",
			Email:      "alice@corp.test",
			Name:       "Alice",
			Role:       identity.RoleAdmin,
		}, nil
	}
	return f, ap
}

func providerID(ap db.AuthProvider) *openapi_types.UUID {
	id := openapi_types.UUID(ap.ID.Bytes)
	return &id
}

func TestLogin_LDAPProvider(t *testing.T) {
	f, ap := ldapLoginFixture(t)
	resp, err := f.h.Login(context.Background(), LoginRequestObject{Body: &LoginRequest{
		ProviderId: providerID(ap),
		Username:   ptr("alice"),
		Password:   "secret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(Login200JSONResponse)
	if !found {
		t.Fatalf("login = %T, want 200", resp)
	}
	if ok.Token == "" || ok.RefreshToken == nil {
		t.Fatalf("login response = %+v", ok)
	}
	if ok.User.Role != "admin" {
		t.Fatalf("role = %q, want the mapped admin role", ok.User.Role)
	}

	var role string
	if err := f.pool.QueryRow(context.Background(), `SELECT role FROM users WHERE external_id = 'stable-1'`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "admin" {
		t.Fatalf("stored role = %q, want admin", role)
	}
}

// TestLogin_LDAPIgnoresEmail: with provider_id the email is not looked up and
// an unrelated address cannot authenticate.
func TestLogin_LDAPIgnoresEmail(t *testing.T) {
	f, ap := ldapLoginFixture(t)
	resp, err := f.h.Login(context.Background(), LoginRequestObject{Body: &LoginRequest{
		ProviderId: providerID(ap),
		Username:   ptr("alice"),
		Password:   "secret",
		Email:      emailPtr("does-not-exist@elsewhere.test"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	ok, found := resp.(Login200JSONResponse)
	if !found {
		t.Fatalf("login = %T, want 200 despite the unrelated email", resp)
	}
	if ok.User.Email != "alice@corp.test" {
		t.Fatalf("user = %+v, want the directory-linked account", ok.User)
	}
}

func TestLogin_LDAPUniformError(t *testing.T) {
	f, ap := ldapLoginFixture(t)

	oidcProvider, err := f.h.queries.CreateAuthProvider(context.Background(), db.CreateAuthProviderParams{
		Name: "sso", Type: "oidc", ConfigEnc: "{}", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		body *LoginRequest
	}{
		{"wrong password", &LoginRequest{ProviderId: providerID(ap), Username: ptr("alice"), Password: "wrong"}},
		{"empty password", &LoginRequest{ProviderId: providerID(ap), Username: ptr("alice"), Password: ""}},
		{"empty username", &LoginRequest{ProviderId: providerID(ap), Password: "secret"}},
		{"unknown directory user", &LoginRequest{ProviderId: providerID(ap), Username: ptr("nobody"), Password: "secret"}},
		{"oidc provider id", &LoginRequest{ProviderId: providerID(oidcProvider), Username: ptr("alice"), Password: "secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := f.h.Login(context.Background(), LoginRequestObject{Body: tc.body})
			if err != nil {
				t.Fatal(err)
			}
			unauthorized, ok := resp.(Login401JSONResponse)
			if !ok {
				t.Fatalf("login = %T, want 401", resp)
			}
			if unauthorized.Code != "invalid_credentials" || unauthorized.Message != "invalid credentials" {
				t.Fatalf("error = %q/%q", unauthorized.Code, unauthorized.Message)
			}
			if strings.Contains(unauthorized.Message, "alice") {
				t.Fatal("error leaks the directory username")
			}
		})
	}
}

// TestLogin_LDAPRejectsNonLDAPAccount: a local account that happens to carry
// the provider/external id pair is not a valid directory link.
func TestLogin_LDAPRejectsNonLDAPAccount(t *testing.T) {
	f, ap := ldapLoginFixture(t)
	f.h.ldapAuthenticate = func(context.Context, identityldap.Config, string, string) (*identityldap.User, error) {
		return &identityldap.User{ExternalID: "local-linked", Role: identity.RoleAdmin}, nil
	}
	hash, err := auth.HashPasswordWithCost("local-password", testBcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO users
		(name, email, role, password_hash, auth_provider, auth_provider_id, external_id)
		VALUES ('Local', 'local@corp.test', 'admin', $1, 'local', $2, 'local-linked')`, hash, ap.ID); err != nil {
		t.Fatal(err)
	}
	resp, err := f.h.Login(context.Background(), LoginRequestObject{Body: &LoginRequest{
		ProviderId: providerID(ap),
		Username:   ptr("local"),
		Password:   "local-password",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(Login401JSONResponse); !ok {
		t.Fatalf("login = %T, want 401", resp)
	}
}

// TestLogin_LDAPNotProvisioned: a directory user without a linked account is
// denied; LDAP never auto-creates by e-mail.
func TestLogin_LDAPNotProvisioned(t *testing.T) {
	f, ap := ldapLoginFixture(t)
	f.h.ldapAuthenticate = func(context.Context, identityldap.Config, string, string) (*identityldap.User, error) {
		return &identityldap.User{ExternalID: "unlinked-1", Email: "new@corp.test", Name: "New", Role: identity.RoleMember}, nil
	}
	resp, err := f.h.Login(context.Background(), LoginRequestObject{Body: &LoginRequest{
		ProviderId: providerID(ap),
		Username:   ptr("new"),
		Password:   "secret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(Login401JSONResponse); !ok {
		t.Fatalf("login = %T, want 401", resp)
	}
}
