package ldap

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/identity"
	ldapv3 "github.com/go-ldap/ldap/v3"
)

func TestNestedGroups(t *testing.T) {
	cfg := validConfig()
	cfg.NestedGroups = true
	cfg.RoleMapping = []identity.RoleMapping{
		{Group: "cn=admins,ou=groups,dc=example,dc=org", Role: identity.RoleAdmin},
		{Group: "cn=direct,ou=groups,dc=example,dc=org", Role: identity.RoleMember},
	}
	user := entry("uid=alice,ou=people,dc=example,dc=org", map[string][]string{
		"entryUUID": {"11111111-1111-1111-1111-111111111111"},
		"memberOf":  {"cn=direct,ou=groups,dc=example,dc=org"},
	})
	nested := &ldapv3.SearchResult{Entries: []*ldapv3.Entry{
		entry("cn=admins,ou=groups,dc=example,dc=org", nil),
		entry("cn=direct,ou=groups,dc=example,dc=org", nil),
	}}
	conn := &fakeConn{searchQueue: []*ldapv3.SearchResult{{Entries: []*ldapv3.Entry{user}}, nested}}
	dial, _ := fakeDialer(conn)

	got, err := authenticate(context.Background(), cfg, "alice", "secret", dial)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.Role != identity.RoleAdmin {
		t.Fatalf("role = %q, want admin", got.Role)
	}
	if len(got.Groups) != 2 {
		t.Fatalf("groups = %v, want direct + nested", got.Groups)
	}
	if len(conn.searches) != 2 {
		t.Fatalf("searches = %d, want user + nested", len(conn.searches))
	}
	wantFilter := "(member:1.2.840.113556.1.4.1941:=" + user.DN + ")"
	if conn.searches[1].Filter != wantFilter {
		t.Fatalf("nested filter = %q", conn.searches[1].Filter)
	}
	if len(conn.binds) != 2 || conn.binds[1] != user.DN || conn.passwords[1] != "secret" {
		t.Fatalf("user bind = %v / %v", conn.binds, conn.passwords)
	}
}

func TestStableIDLinking(t *testing.T) {
	t.Run("entryUUID", func(t *testing.T) {
		id, err := stableID(entry("uid=alice,ou=people,dc=example,dc=org", map[string][]string{
			"entryUUID": {"11111111-1111-1111-1111-111111111111"},
		}))
		if err != nil {
			t.Fatalf("stableID: %v", err)
		}
		if id != "11111111-1111-1111-1111-111111111111" {
			t.Fatalf("id = %q", id)
		}
	})

	t.Run("objectGUID", func(t *testing.T) {
		raw := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
		e := ldapv3.NewEntry("cn=Alice,ou=people,dc=example,dc=org", nil)
		e.Attributes = append(e.Attributes, &ldapv3.EntryAttribute{
			Name:       "objectGUID",
			ByteValues: [][]byte{raw},
		})
		id, err := stableID(e)
		if err != nil {
			t.Fatalf("stableID: %v", err)
		}
		if id != "03020100-0504-0706-0809-0a0b0c0d0e0f" {
			t.Fatalf("id = %q", id)
		}
	})

	t.Run("missing", func(t *testing.T) {
		if _, err := stableID(entry("uid=alice,ou=people,dc=example,dc=org", nil)); err == nil {
			t.Fatal("entry without a stable id was accepted")
		}
	})
}

func TestRequiredGroup(t *testing.T) {
	cfg := validConfig()
	cfg.RequiredGroup = "cn=admins,ou=groups,dc=example,dc=org"

	user := entry("uid=alice,ou=people,dc=example,dc=org", map[string][]string{
		"entryUUID": {"11111111-1111-1111-1111-111111111111"},
		"memberOf":  {"cn=direct,ou=groups,dc=example,dc=org"},
	})
	conn := &fakeConn{searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{user}}}
	dial, _ := fakeDialer(conn)
	if _, err := authenticate(context.Background(), cfg, "alice", "secret", dial); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("authenticate = %v, want ErrInvalidCredentials", err)
	}

	admin := entry("uid=alice,ou=people,dc=example,dc=org", map[string][]string{
		"entryUUID": {"11111111-1111-1111-1111-111111111111"},
		"memberOf": {
			"cn=direct,ou=groups,dc=example,dc=org",
			"cn=admins,ou=groups,dc=example,dc=org",
		},
	})
	conn = &fakeConn{searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{admin}}}
	dial, _ = fakeDialer(conn)
	if _, err := authenticate(context.Background(), cfg, "alice", "secret", dial); err != nil {
		t.Fatalf("authenticate with required group: %v", err)
	}
}

func TestUserBindRejectsWrongPassword(t *testing.T) {
	user := entry("uid=alice,ou=people,dc=example,dc=org", map[string][]string{
		"entryUUID": {"11111111-1111-1111-1111-111111111111"},
	})
	conn := &fakeConn{
		searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{user}},
		userBindErr:  errors.New("invalid credentials"),
	}
	dial, _ := fakeDialer(conn)
	if _, err := authenticate(context.Background(), validConfig(), "alice", "wrong", dial); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("authenticate = %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthenticateUsesDefaultRoleAndName(t *testing.T) {
	user := entry("uid=alice,ou=people,dc=example,dc=org", map[string][]string{
		"entryUUID": {"11111111-1111-1111-1111-111111111111"},
		"mail":      {"alice@example.org"},
		"cn":        {"Alice"},
	})
	conn := &fakeConn{searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{user}}}
	dial, _ := fakeDialer(conn)
	got, err := authenticate(context.Background(), validConfig(), "alice", "secret", dial)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.Role != identity.RoleMember || got.Email != "alice@example.org" || got.Name != "Alice" {
		t.Fatalf("user = %+v", got)
	}
}
