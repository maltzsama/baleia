// Package ldap authenticates users against an LDAP directory (AD or
// OpenLDAP). It is experimental: the provider can be configured, but the
// feature is not covered by a support commitment yet.
package ldap

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/identity"
)

const (
	// DefaultTimeoutSeconds bounds a directory call when the config does not
	// set timeout_seconds.
	DefaultTimeoutSeconds = 10
	// MaxTimeoutSeconds is the largest accepted timeout_seconds.
	MaxTimeoutSeconds = 60

	defaultGroupAttribute = "memberOf"
	defaultEmailAttribute = "mail"
	defaultNameAttribute  = "cn"
)

// Config is the typed shape of an LDAP auth_providers.config_enc value. It is
// decoded strictly, validated, and sealed with the keyring before storage.
type Config struct {
	URL string `json:"url"`
	// StartTLS upgrades an ldap:// connection. It is required for ldap:// and
	// ignored (the connection is already TLS) for ldaps://.
	StartTLS bool `json:"start_tls"`
	// CAPem is an optional PEM bundle used to verify a private CA, such as the
	// self-signed certificate of a development directory. Empty uses the
	// system roots.
	CAPem string `json:"ca_pem"`

	// BindDN/BindPassword are the service account used to search the directory.
	// They never authenticate a user; the user bind uses the submitted
	// password.
	BindDN       string `json:"bind_dn"`
	BindPassword string `json:"bind_password"`

	BaseDN     string `json:"base_dn"`
	UserFilter string `json:"user_filter"`

	// Attributes read from the directory entry. Empty values fall back to the
	// defaults (memberOf, mail, cn).
	GroupAttribute string `json:"group_attribute"`
	EmailAttribute string `json:"email_attribute"`
	NameAttribute  string `json:"name_attribute"`

	// NestedGroups resolves group membership through the AD recursive
	// matching rule 1.2.840.113556.1.4.1941.
	NestedGroups bool `json:"nested_groups"`
	// RequiredGroup, when set, must be one of the user's groups.
	RequiredGroup string `json:"required_group"`

	TimeoutSeconds int `json:"timeout_seconds"`

	RoleMapping []identity.RoleMapping `json:"role_mapping"`
	TeamMapping []identity.TeamMapping `json:"team_mapping"`
	DefaultRole string                 `json:"default_role"`
}

// Normalize fills defaults. It is applied after decoding and before
// validation, so the stored config is explicit.
func (c Config) Normalize() Config {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if c.GroupAttribute == "" {
		c.GroupAttribute = defaultGroupAttribute
	}
	if c.EmailAttribute == "" {
		c.EmailAttribute = defaultEmailAttribute
	}
	if c.NameAttribute == "" {
		c.NameAttribute = defaultNameAttribute
	}
	return c
}

// Validate rejects insecure or incomplete configurations. In particular, a
// plaintext ldap:// URL is only accepted with StartTLS.
func (c Config) Validate() error {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return fmt.Errorf("ldap config: url: %w", err)
	}
	if u.Host == "" {
		return errors.New("ldap config: url must include a host")
	}
	if u.User != nil {
		return errors.New("ldap config: url must not contain credentials")
	}
	switch strings.ToLower(u.Scheme) {
	case "ldaps":
	case "ldap":
		if !c.StartTLS {
			return errors.New("ldap config: ldap:// requires start_tls; use ldaps:// or enable StartTLS")
		}
	default:
		return fmt.Errorf("ldap config: unsupported scheme %q (use ldaps:// or ldap:// with start_tls)", u.Scheme)
	}

	if strings.TrimSpace(c.CAPem) != "" {
		if _, err := parseCAs(c.CAPem); err != nil {
			return fmt.Errorf("ldap config: ca_pem: %w", err)
		}
	}
	if strings.TrimSpace(c.BindDN) == "" {
		return errors.New("ldap config: bind_dn is required")
	}
	if c.BindPassword == "" {
		return errors.New("ldap config: bind_password is required")
	}
	if strings.TrimSpace(c.BaseDN) == "" {
		return errors.New("ldap config: base_dn is required")
	}
	if strings.TrimSpace(c.UserFilter) == "" {
		return errors.New("ldap config: user_filter is required")
	}
	if strings.Count(c.UserFilter, "{{username}}") != 1 {
		return errors.New("ldap config: user_filter must contain exactly one {{username}} placeholder")
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("ldap config: timeout_seconds must be between 1 and %d", MaxTimeoutSeconds)
	}
	if c.DefaultRole != "" && !validRole(c.DefaultRole) {
		return fmt.Errorf("ldap config: default_role %q is not %q or %q", c.DefaultRole, identity.RoleAdmin, identity.RoleMember)
	}
	for i, m := range c.RoleMapping {
		if strings.TrimSpace(m.Group) == "" {
			return fmt.Errorf("ldap config: role_mapping %d has an empty group", i)
		}
		if !validRole(m.Role) {
			return fmt.Errorf("ldap config: role_mapping %d has invalid role %q", i, m.Role)
		}
	}
	for i, m := range c.TeamMapping {
		if strings.TrimSpace(m.Group) == "" {
			return fmt.Errorf("ldap config: team_mapping %d has an empty group", i)
		}
		if strings.TrimSpace(m.TeamID) == "" {
			return fmt.Errorf("ldap config: team_mapping %d has an empty team id", i)
		}
		if !validTeamRole(m.TeamRole) {
			return fmt.Errorf("ldap config: team_mapping %d has invalid team role %q", i, m.TeamRole)
		}
	}
	return nil
}

// parseCAs builds a trust pool from a PEM bundle. It is used by dialLDAP so a
// private or development CA can be pinned in the provider config.
func parseCAs(pemBundle string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(pemBundle)) {
		return nil, errors.New("no PEM certificate found")
	}
	return pool, nil
}

func validRole(role string) bool {
	return role == identity.RoleAdmin || role == identity.RoleMember
}

func validTeamRole(role string) bool {
	return role == identity.TeamRoleOwner || role == identity.TeamRoleMember
}

// Decode strictly decodes and validates a config. Unknown keys are rejected
// (#253), so a typo cannot silently disable a security setting.
func Decode(raw []byte) (Config, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("ldap config: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("ldap config: trailing data after JSON object")
	}
	c = c.Normalize()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// SealConfig encrypts the config with the keyring. aad is the provider id, so
// a config copied to another provider does not open.
func SealConfig(kr *crypto.Keyring, aad string, cfg Config) (string, error) {
	if kr == nil {
		return "", errors.New("ldap config: keyring is required")
	}
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("ldap config: %w", err)
	}
	return kr.Seal(raw, aad)
}

// OpenConfig decrypts and validates a stored config. It fails closed: a
// plaintext or corrupt value never falls back to JSON.
func OpenConfig(kr *crypto.Keyring, aad, envelope string) (Config, error) {
	if kr == nil {
		return Config{}, errors.New("ldap config: keyring is required")
	}
	raw, err := kr.Open(envelope, aad)
	if err != nil {
		return Config{}, fmt.Errorf("ldap config: %w", err)
	}
	return Decode(raw)
}
