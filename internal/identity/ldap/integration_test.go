//go:build integration

package ldap

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/identity"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TestIntegration_OpenLDAP runs against `docker compose --profile ldap up -d
// ldap` (#282). It verifies the user bind and that the seeded
// cn=baleia-admins group maps to the admin role.
//
//	BALEIA_TEST_LDAP_URL=ldap://localhost:389 \
//	  go test -tags integration ./internal/identity/ldap/
func TestIntegration_OpenLDAP(t *testing.T) {
	t.Parallel()
	url := os.Getenv("BALEIA_TEST_LDAP_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_LDAP_URL not set; start the `ldap` compose profile")
	}

	const adminGroup = "cn=baleia-admins,ou=groups,dc=example,dc=org"
	cfg := Config{
		// The seed profile is plaintext on localhost. Production configs are
		// validated by OpenConfig and must use ldaps:// or StartTLS.
		URL:            url,
		BindDN:         envOr("BALEIA_TEST_LDAP_BIND_DN", "cn=admin,dc=example,dc=org"),
		BindPassword:   envOr("BALEIA_TEST_LDAP_BIND_PASSWORD", "admin"),
		BaseDN:         envOr("BALEIA_TEST_LDAP_BASE_DN", "ou=people,dc=example,dc=org"),
		UserFilter:     envOr("BALEIA_TEST_LDAP_USER_FILTER", "(uid={{username}})"),
		RequiredGroup:  adminGroup,
		RoleMapping:    []identity.RoleMapping{{Group: adminGroup, Role: identity.RoleAdmin}},
		TimeoutSeconds: 10,
	}.Normalize()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	user, err := Authenticate(ctx, cfg,
		envOr("BALEIA_TEST_LDAP_USER", "alice"),
		envOr("BALEIA_TEST_LDAP_PASSWORD", "ldap-password"))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if user.ExternalID == "" {
		t.Fatal("no stable id (entryUUID/objectGUID) returned")
	}
	if user.Role != identity.RoleAdmin {
		t.Fatalf("role = %q, want admin from %s", user.Role, adminGroup)
	}
	if !containsGroup(user.Groups, adminGroup) {
		t.Fatalf("groups = %v, want %s", user.Groups, adminGroup)
	}

	// A wrong password is the uniform failure.
	if _, err := Authenticate(ctx, cfg, "alice", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password = %v, want ErrInvalidCredentials", err)
	}
}
