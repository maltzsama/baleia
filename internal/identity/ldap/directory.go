package ldap

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

// directoryPageSize is the LDAP page size of one reconciliation pass (#257).
const directoryPageSize = 500

// DirectoryUser is the reconciler's view of one directory entry.
type DirectoryUser struct {
	ExternalID string
	Disabled   bool
	Groups     []string
}

// ListUsers pages the whole directory as the service account. It never binds
// as a user. A malformed entry fails the pass instead of being treated as
// missing, so a parsing bug can never mass-suspend accounts.
func ListUsers(ctx context.Context, cfg Config) ([]DirectoryUser, error) {
	return listUsers(ctx, cfg, dialLDAP)
}

func listUsers(ctx context.Context, cfg Config, dial dialFunc) ([]DirectoryUser, error) {
	conn, err := dial(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return nil, fmt.Errorf("ldap: service bind: %w", err)
	}

	// Replace the single {{username}} placeholder with an LDAP wildcard. The
	// template is admin-controlled; no user input reaches this filter.
	filter := strings.ReplaceAll(cfg.UserFilter, "{{username}}", "*")
	attrs := append(entryAttributes(cfg), "userAccountControl")
	result, err := conn.SearchWithPaging(&ldapv3.SearchRequest{
		BaseDN:       cfg.BaseDN,
		Scope:        ldapv3.ScopeWholeSubtree,
		DerefAliases: ldapv3.NeverDerefAliases,
		SizeLimit:    0,
		TimeLimit:    cfg.TimeoutSeconds,
		TypesOnly:    false,
		Filter:       filter,
		Attributes:   attrs,
	}, directoryPageSize)
	if err != nil {
		return nil, fmt.Errorf("ldap: list users: %w", err)
	}

	out := make([]DirectoryUser, 0, len(result.Entries))
	for _, e := range result.Entries {
		id, err := stableID(e)
		if err != nil {
			return nil, fmt.Errorf("ldap: list users: %w", err)
		}
		out = append(out, DirectoryUser{
			ExternalID: id,
			Disabled:   disabledEntry(e),
			Groups:     groupValues(e, cfg.GroupAttribute),
		})
	}
	return out, nil
}

// disabledEntry reads AD's userAccountControl disabled bit (0x2). OpenLDAP
// entries do not publish the attribute, so they are treated as enabled.
func disabledEntry(e *ldapv3.Entry) bool {
	raw := strings.TrimSpace(e.GetEqualFoldAttributeValue("userAccountControl"))
	if raw == "" {
		return false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return false
	}
	return n&2 != 0
}
