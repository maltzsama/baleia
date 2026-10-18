package ldap

import (
	"context"
	"strings"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

// Probe status values, matching the admin API's check vocabulary (#286).
const (
	ProbeOK      = "ok"
	ProbeFailed  = "failed"
	ProbeSkipped = "skipped"
)

// ProbeCheck is one step of a provider probe. Message is a short stable code,
// never a raw error, host or credential.
type ProbeCheck struct {
	Name    string
	Status  string
	Message string
}

// Probe validates the connection and the service bind, then optionally looks
// up one directory username. It never binds as the end user: the submitted
// test username is only searched for, so a probe can never authenticate.
func Probe(ctx context.Context, cfg Config, testUsername string) []ProbeCheck {
	return probe(ctx, cfg, testUsername, dialLDAP)
}

func probe(ctx context.Context, cfg Config, testUsername string, dial dialFunc) []ProbeCheck {
	checks := make([]ProbeCheck, 0, 3)

	conn, err := dial(ctx, cfg)
	if err != nil {
		return append(checks, ProbeCheck{Name: "tls", Status: ProbeFailed, Message: "tls_handshake_failed"})
	}
	defer conn.Close()
	checks = append(checks, ProbeCheck{Name: "tls", Status: ProbeOK})

	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return append(checks, ProbeCheck{Name: "service_bind", Status: ProbeFailed, Message: "service_bind_failed"})
	}
	checks = append(checks, ProbeCheck{Name: "service_bind", Status: ProbeOK})

	username := strings.TrimSpace(testUsername)
	if username == "" {
		return append(checks, ProbeCheck{Name: "user_search", Status: ProbeSkipped, Message: "no_test_username"})
	}
	filter, err := userFilter(cfg.UserFilter, username)
	if err != nil {
		return append(checks, ProbeCheck{Name: "user_search", Status: ProbeFailed, Message: "invalid_user_filter"})
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
		return append(checks, ProbeCheck{Name: "user_search", Status: ProbeFailed, Message: "search_failed"})
	}
	if len(result.Entries) != 1 {
		return append(checks, ProbeCheck{Name: "user_search", Status: ProbeFailed, Message: "user_not_found"})
	}
	return append(checks, ProbeCheck{Name: "user_search", Status: ProbeOK})
}
