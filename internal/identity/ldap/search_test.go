package ldap

import (
	"context"
	"errors"
	"testing"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

type fakeConn struct {
	binds        []string
	searches     []*ldapv3.SearchRequest
	bindErr      error
	searchErr    error
	searchResult *ldapv3.SearchResult
	closed       bool
}

func (f *fakeConn) Bind(username, password string) error {
	f.binds = append(f.binds, username)
	return f.bindErr
}

func (f *fakeConn) Search(req *ldapv3.SearchRequest) (*ldapv3.SearchResult, error) {
	f.searches = append(f.searches, req)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if f.searchResult != nil {
		return f.searchResult, nil
	}
	return &ldapv3.SearchResult{}, nil
}

func (f *fakeConn) Close() error {
	f.closed = true
	return nil
}

func fakeDialer(conn *fakeConn) (dialFunc, *int) {
	calls := 0
	return func(context.Context, Config) (Conn, error) {
		calls++
		return conn, nil
	}, &calls
}

func entry(dn string, attrs map[string][]string) *ldapv3.Entry {
	return ldapv3.NewEntry(dn, attrs)
}

func TestFilterInjection(t *testing.T) {
	escaped := ldapv3.EscapeFilter(`*)(uid=*`)
	filter, err := userFilter("(uid={{username}})", `*)(uid=*`)
	if err != nil {
		t.Fatalf("userFilter: %v", err)
	}
	if filter != "(uid="+escaped+")" {
		t.Fatalf("filter = %q, want escaped value", filter)
	}
	if filter == `(uid=*)(uid=*)` {
		t.Fatal("filter was built by concatenation")
	}

	// The regex also rejects the malicious username before any dial.
	conn := &fakeConn{}
	dial, calls := fakeDialer(conn)
	if _, err := findUser(context.Background(), validConfig(), `*)(uid=*`, "secret", dial); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("findUser = %v, want ErrInvalidCredentials", err)
	}
	if *calls != 0 {
		t.Fatalf("dial called %d times for an invalid username", *calls)
	}
}

func TestEmptyPasswordRejected(t *testing.T) {
	conn := &fakeConn{}
	dial, calls := fakeDialer(conn)
	if _, err := findUser(context.Background(), validConfig(), "alice", "", dial); !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("findUser = %v, want ErrEmptyPassword", err)
	}
	if *calls != 0 || len(conn.binds) != 0 {
		t.Fatalf("empty password reached dial/bind (dial=%d binds=%d)", *calls, len(conn.binds))
	}
}

func TestMultipleMatchesRejected(t *testing.T) {
	conn := &fakeConn{searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{
		entry("uid=one,ou=people,dc=example,dc=org", nil),
		entry("uid=two,ou=people,dc=example,dc=org", nil),
	}}}
	dial, _ := fakeDialer(conn)
	if _, err := findUser(context.Background(), validConfig(), "alice", "secret", dial); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("findUser = %v, want ErrInvalidCredentials", err)
	}
	if !conn.closed {
		t.Fatal("connection was not closed")
	}
}

func TestFindUser_SingleMatch(t *testing.T) {
	conn := &fakeConn{searchResult: &ldapv3.SearchResult{Entries: []*ldapv3.Entry{
		entry("uid=alice,ou=people,dc=example,dc=org", nil),
	}}}
	dial, _ := fakeDialer(conn)
	got, err := findUser(context.Background(), validConfig(), "alice", "secret", dial)
	if err != nil {
		t.Fatalf("findUser: %v", err)
	}
	if got.DN != "uid=alice,ou=people,dc=example,dc=org" {
		t.Fatalf("DN = %q", got.DN)
	}
	if len(conn.binds) != 1 || conn.binds[0] != validConfig().BindDN {
		t.Fatalf("service bind = %v", conn.binds)
	}
	if len(conn.searches) != 1 {
		t.Fatalf("searches = %d", len(conn.searches))
	}
	req := conn.searches[0]
	if req.Filter != "(uid=alice)" || req.SizeLimit != 2 {
		t.Fatalf("search filter=%q size=%d", req.Filter, req.SizeLimit)
	}
}
