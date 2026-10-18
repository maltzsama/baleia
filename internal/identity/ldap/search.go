package ldap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

var (
	// ErrInvalidCredentials is returned for every authentication failure. The
	// caller must not distinguish "unknown user" from "wrong password".
	ErrInvalidCredentials = errors.New("ldap: invalid credentials")
	// ErrEmptyPassword means the submitted password was empty. LDAP treats an
	// empty-password bind as an anonymous bind, which can succeed, so the
	// adapter rejects it before any dial or bind (#254).
	ErrEmptyPassword = errors.New("ldap: empty password")
)

// usernameRE is the only shape accepted as a directory username. It excludes
// everything LDAP filter metacharacters would need, but the filter is escaped
// anyway (#254).
var usernameRE = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,128}$`)

// Conn is the subset of *ldap.Conn the adapter uses. Tests substitute a fake.
type Conn interface {
	Bind(username, password string) error
	Search(req *ldapv3.SearchRequest) (*ldapv3.SearchResult, error)
	Close() error
}

// dialFunc opens a connection. Tests substitute a fake.
type dialFunc func(ctx context.Context, cfg Config) (Conn, error)

// dialLDAP opens a TLS connection (ldaps://) or a plain connection upgraded
// with StartTLS (ldap:// with start_tls).
func dialLDAP(ctx context.Context, cfg Config) (Conn, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("ldap: url: %w", err)
	}
	tlsConfig := &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}

	conn, err := ldapv3.DialURL(cfg.URL,
		ldapv3.DialWithDialer(&net.Dialer{Timeout: timeout}),
		ldapv3.DialWithTLSConfig(tlsConfig),
	)
	if err != nil {
		return nil, fmt.Errorf("ldap: dial: %w", err)
	}
	conn.SetTimeout(timeout)

	if strings.EqualFold(u.Scheme, "ldap") && cfg.StartTLS {
		if err := conn.StartTLS(tlsConfig); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ldap: starttls: %w", err)
		}
	}
	return conn, nil
}

// openAndFind validates the submitted credentials, binds as the service
// account, and searches for exactly one user. The connection is returned open
// for the user bind (#281) and must be closed by the caller.
func openAndFind(ctx context.Context, cfg Config, username, password string, dial dialFunc) (Conn, *ldapv3.Entry, error) {
	if password == "" {
		return nil, nil, ErrEmptyPassword
	}
	if !usernameRE.MatchString(username) {
		return nil, nil, ErrInvalidCredentials
	}

	conn, err := dial(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("ldap: service bind: %w", err)
	}

	filter, err := userFilter(cfg.UserFilter, username)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	result, err := conn.Search(&ldapv3.SearchRequest{
		BaseDN:       cfg.BaseDN,
		Scope:        ldapv3.ScopeWholeSubtree,
		DerefAliases: ldapv3.NeverDerefAliases,
		SizeLimit:    2,
		TimeLimit:    cfg.TimeoutSeconds,
		TypesOnly:    false,
		Filter:       filter,
		Attributes:   entryAttributes(cfg),
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("ldap: search: %w", err)
	}
	// Zero matches and ambiguous matches are the same generic failure.
	if len(result.Entries) != 1 {
		_ = conn.Close()
		return nil, nil, ErrInvalidCredentials
	}
	return conn, result.Entries[0], nil
}

// findUser is the search half of the flow (#254). The user bind is separate.
func findUser(ctx context.Context, cfg Config, username, password string, dial dialFunc) (*ldapv3.Entry, error) {
	conn, entry, err := openAndFind(ctx, cfg, username, password, dial)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return entry, nil
}

// userFilter substitutes the username into the admin-controlled template. Raw
// input is escaped with EscapeFilter; it is never formatted into the filter
// with fmt.Sprintf (#254).
func userFilter(template, username string) (string, error) {
	if strings.Count(template, "{{username}}") != 1 {
		return "", errors.New("ldap: user_filter must contain exactly one {{username}} placeholder")
	}
	return strings.ReplaceAll(template, "{{username}}", ldapv3.EscapeFilter(username)), nil
}

func entryAttributes(cfg Config) []string {
	attrs := []string{cfg.EmailAttribute, cfg.NameAttribute, cfg.GroupAttribute, "entryUUID", "objectGUID"}
	seen := make(map[string]struct{}, len(attrs))
	out := attrs[:0]
	for _, a := range attrs {
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}
