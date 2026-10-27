package ldap

import (
	"context"
	"testing"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

func TestListUsersParsesDisabledAndGroups(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	enabled := entry("uid=alice,ou=people,dc=example,dc=org", map[string][]string{
		"entryUUID":          {"11111111-1111-1111-1111-111111111111"},
		"userAccountControl": {"512"},
		"memberOf":           {"cn=eng,ou=groups,dc=example,dc=org"},
	})
	disabled := entry("uid=bob,ou=people,dc=example,dc=org", map[string][]string{
		"entryUUID":          {"22222222-2222-2222-2222-222222222222"},
		"userAccountControl": {"514"},
	})
	conn := &fakeConn{searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{enabled, disabled}}}
	dial, _ := fakeDialer(conn)

	users, err := listUsers(context.Background(), cfg, dial)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %d, want 2", len(users))
	}
	if users[0].Disabled || !users[1].Disabled {
		t.Fatalf("disabled flags = %v/%v", users[0].Disabled, users[1].Disabled)
	}
	if len(users[0].Groups) != 1 || users[0].Groups[0] != "cn=eng,ou=groups,dc=example,dc=org" {
		t.Fatalf("groups = %v", users[0].Groups)
	}
	if len(conn.binds) != 1 || conn.binds[0] != cfg.BindDN {
		t.Fatalf("binds = %v, want only the service bind", conn.binds)
	}
	if len(conn.searches) != 1 || conn.searches[0].Filter != "(uid=*)" {
		t.Fatalf("filter = %+v, want (uid=*)", conn.searches)
	}
}

func TestListUsersFailsClosedOnMissingStableID(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	bad := entry("uid=noid,ou=people,dc=example,dc=org", map[string][]string{
		"userAccountControl": {"512"},
	})
	conn := &fakeConn{searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{bad}}}
	dial, _ := fakeDialer(conn)
	if _, err := listUsers(context.Background(), cfg, dial); err == nil {
		t.Fatal("an entry without a stable id was accepted")
	}
}
