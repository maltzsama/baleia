package ldap

import (
	"context"
	"testing"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

// TestProbeNeverUserBinds: a probe searches for the test username with the
// service account and never binds as that user, so it cannot authenticate.
func TestProbeNeverUserBinds(t *testing.T) {
	cfg := validConfig()
	user := entry("uid=alice,ou=people,dc=example,dc=org", map[string][]string{
		"entryUUID": {"11111111-1111-1111-1111-111111111111"},
	})
	conn := &fakeConn{searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{user}}}
	dial, _ := fakeDialer(conn)

	checks := probe(context.Background(), cfg, "alice", dial)
	for _, c := range checks {
		if c.Status != ProbeOK {
			t.Fatalf("check %q = %s (%s)", c.Name, c.Status, c.Message)
		}
	}
	if len(conn.binds) != 1 || conn.binds[0] != cfg.BindDN {
		t.Fatalf("binds = %v, want only the service bind %q", conn.binds, cfg.BindDN)
	}
	if len(conn.searches) != 1 || conn.searches[0].Filter != "(uid=alice)" {
		t.Fatalf("searches = %+v", conn.searches)
	}
}

// TestProbeSkipsSearchWithoutUsername: no test user means no search at all.
func TestProbeSkipsSearchWithoutUsername(t *testing.T) {
	cfg := validConfig()
	conn := &fakeConn{}
	dial, _ := fakeDialer(conn)

	checks := probe(context.Background(), cfg, "", dial)
	if len(checks) != 3 || checks[2].Status != ProbeSkipped {
		t.Fatalf("checks = %+v", checks)
	}
	if len(conn.searches) != 0 {
		t.Fatalf("searches = %d, want 0", len(conn.searches))
	}
}
