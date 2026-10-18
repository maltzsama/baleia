package ldap

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/identity"
	ldapv3 "github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
)

// User is the directory identity after a successful bind and group mapping.
type User struct {
	// ExternalID is objectGUID (AD) or entryUUID (OpenLDAP). Accounts are
	// linked by this stable id, never by DN or e-mail (#281).
	ExternalID string
	Email      string
	Name       string
	Groups     []string
	Role       string
	Teams      []identity.TeamAssignment
}

// Authenticate verifies the submitted password with a user bind and resolves
// the user's groups and platform mapping (#281).
func Authenticate(ctx context.Context, cfg Config, username, password string) (*User, error) {
	return authenticate(ctx, cfg, username, password, dialLDAP)
}

func authenticate(ctx context.Context, cfg Config, username, password string, dial dialFunc) (*User, error) {
	conn, entry, err := openAndFind(ctx, cfg, username, password, dial)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// The service bind only proves the service account can read. The user bind
	// is what verifies the submitted password.
	if err := conn.Bind(entry.DN, password); err != nil {
		return nil, ErrInvalidCredentials
	}

	groups := groupValues(entry, cfg.GroupAttribute)
	if cfg.NestedGroups {
		nested, err := nestedGroups(conn, cfg, entry.DN)
		if err != nil {
			return nil, err
		}
		groups = mergeGroups(groups, nested)
	}
	if cfg.RequiredGroup != "" && !containsGroup(groups, cfg.RequiredGroup) {
		return nil, ErrInvalidCredentials
	}

	externalID, err := stableID(entry)
	if err != nil {
		return nil, err
	}
	mapped, err := identity.MapIdentity(identity.MappingInput{
		Groups:      groups,
		RoleMapping: cfg.RoleMapping,
		TeamMapping: cfg.TeamMapping,
		DefaultRole: cfg.DefaultRole,
	})
	if err != nil {
		return nil, err
	}

	return &User{
		ExternalID: externalID,
		Email:      strings.TrimSpace(entry.GetEqualFoldAttributeValue(cfg.EmailAttribute)),
		Name:       strings.TrimSpace(entry.GetEqualFoldAttributeValue(cfg.NameAttribute)),
		Groups:     groups,
		Role:       mapped.Role,
		Teams:      mapped.Teams,
	}, nil
}

// nestedGroups resolves transitive membership with the AD recursive matching
// rule 1.2.840.113556.1.4.1941. OpenLDAP does not implement this rule, so the
// setting is documented as AD-only; a failed query fails the login closed.
func nestedGroups(conn Conn, cfg Config, userDN string) ([]string, error) {
	filter := "(member:1.2.840.113556.1.4.1941:=" + ldapv3.EscapeFilter(userDN) + ")"
	result, err := conn.Search(&ldapv3.SearchRequest{
		BaseDN:       cfg.BaseDN,
		Scope:        ldapv3.ScopeWholeSubtree,
		DerefAliases: ldapv3.NeverDerefAliases,
		SizeLimit:    1000,
		TimeLimit:    cfg.TimeoutSeconds,
		TypesOnly:    false,
		Filter:       filter,
		Attributes:   []string{"cn"},
	})
	if err != nil {
		return nil, fmt.Errorf("ldap: nested groups: %w", err)
	}
	out := make([]string, 0, len(result.Entries))
	for _, e := range result.Entries {
		out = append(out, e.DN)
	}
	return out, nil
}

func groupValues(entry *ldapv3.Entry, attribute string) []string {
	values := entry.GetEqualFoldAttributeValues(attribute)
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func mergeGroups(base, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, g := range append(append([]string{}, base...), extra...) {
		if _, ok := seen[g]; ok {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	return out
}

func containsGroup(groups []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, g := range groups {
		if strings.ToLower(strings.TrimSpace(g)) == want {
			return true
		}
	}
	return false
}

// stableID prefers entryUUID (OpenLDAP) and falls back to objectGUID (AD).
func stableID(entry *ldapv3.Entry) (string, error) {
	if v := strings.TrimSpace(entry.GetEqualFoldAttributeValue("entryUUID")); v != "" {
		return v, nil
	}
	if raw := entry.GetEqualFoldRawAttributeValue("objectGUID"); len(raw) > 0 {
		return objectGUID(raw), nil
	}
	return "", errors.New("ldap: entry has neither entryUUID nor objectGUID")
}

// objectGUID renders AD's binary GUID. Its first three components are
// little-endian, so reorder them before formatting as a UUID.
func objectGUID(raw []byte) string {
	if len(raw) != 16 {
		return hex.EncodeToString(raw)
	}
	var u uuid.UUID
	u[0], u[1], u[2], u[3] = raw[3], raw[2], raw[1], raw[0]
	u[4], u[5] = raw[5], raw[4]
	u[6], u[7] = raw[7], raw[6]
	copy(u[8:], raw[8:])
	return u.String()
}
