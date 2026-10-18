package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Agent tokens are bearer credentials minted by a user for one agent (#59).
// They carry the prefix below so every non-/mcp surface can reject them
// explicitly instead of trying to parse them as a JWT.
const AgentTokenPrefix = "bal_agt_"

// Agent token defaults and TTL bounds come from configuration; these are the
// values the issue specifies when nothing is configured.
const (
	DefaultAgentTokenTTL = 30 * 24 * time.Hour
	MaxAgentTokenTTL     = 90 * 24 * time.Hour
)

// Token rejection reasons. They are bounded so they can be audited and
// counted without leaking which check failed to the caller.
const (
	AgentRejectUnknown        = "unknown"
	AgentRejectExpired        = "expired"
	AgentRejectRevoked        = "revoked"
	AgentRejectAgentDisabled  = "agent_disabled"
	AgentRejectOwnerSuspended = "owner_suspended"
)

var (
	// ErrAgentTokenNotAllowed is returned when an agent token reaches a surface
	// other than /mcp.
	ErrAgentTokenNotAllowed = errors.New("auth: agent token is only valid on /mcp")
	// ErrAgentTokenInvalid covers unknown and malformed tokens.
	ErrAgentTokenInvalid = errors.New("auth: invalid agent token")
	// ErrAgentTokenExpired means the token is past expires_at.
	ErrAgentTokenExpired = errors.New("auth: agent token expired")
	// ErrAgentTokenRevoked means the token was revoked.
	ErrAgentTokenRevoked = errors.New("auth: agent token revoked")
	// ErrAgentDisabled means the owning agent is disabled.
	ErrAgentDisabled = errors.New("auth: agent disabled")
	// ErrAgentOwnerSuspended means the agent's owner is suspended.
	ErrAgentOwnerSuspended = errors.New("auth: agent owner suspended")
)

// IsAgentToken reports whether raw carries the agent-token prefix.
func IsAgentToken(raw string) bool {
	return strings.HasPrefix(raw, AgentTokenPrefix)
}

// NewAgentToken returns the one-time secret, its lookup hash and the display
// prefix. The raw secret is never stored.
func NewAgentToken() (secret, hash, prefix string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	secret = AgentTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return secret, AgentTokenHash(secret), AgentTokenDisplayPrefix(secret)
}

// AgentTokenHash is the lookup hash for an agent token.
func AgentTokenHash(raw string) string { return HashAPIKey(raw) }

// AgentTokenDisplayPrefix is the shareable prefix stored for display. It never
// reveals enough of the secret to authenticate.
func AgentTokenDisplayPrefix(raw string) string {
	const keep = len(AgentTokenPrefix) + 8
	if len(raw) > keep {
		return raw[:keep]
	}
	return raw
}
