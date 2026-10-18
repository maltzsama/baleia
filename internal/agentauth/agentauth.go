// Package agentauth resolves an agent bearer token to its owner and agent
// (#59). It lives outside internal/auth because the generated db package
// imports internal/ws, which imports internal/auth: putting the query here
// keeps that dependency graph acyclic.
package agentauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the slice of the database agent authentication reads.
type Store interface {
	GetAgentTokenByHash(ctx context.Context, tokenHash string) (db.AgentToken, error)
	GetAgentByID(ctx context.Context, id pgtype.UUID) (db.Agent, error)
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
	TouchAgentToken(ctx context.Context, id pgtype.UUID) error
}

// Identity is what a valid agent token resolves to. OwnerID is the user the
// agent acts for; an agent never has an identity of its own.
type Identity struct {
	AgentID pgtype.UUID
	OwnerID pgtype.UUID
	Kind    string
}

// AuthError carries the bounded rejection reason and the token prefix, so the
// caller can audit `agent_token_rejected` without storing the secret.
type AuthError struct {
	Prefix string
	Reason string
	Err    error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("auth: agent token %s: %s", e.Prefix, e.Reason)
}

func (e *AuthError) Unwrap() error { return e.Err }

// Authenticate resolves a raw agent token against the database. It does not
// cache: revocation, disablement and owner suspension take effect on the next
// request.
func Authenticate(ctx context.Context, q Store, raw string) (Identity, error) {
	prefix := auth.AgentTokenDisplayPrefix(raw)
	fail := func(reason string, err error) (Identity, error) {
		return Identity{}, &AuthError{Prefix: prefix, Reason: reason, Err: err}
	}

	if !auth.IsAgentToken(raw) {
		return fail(auth.AgentRejectUnknown, auth.ErrAgentTokenInvalid)
	}

	tok, err := q.GetAgentTokenByHash(ctx, auth.AgentTokenHash(raw))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(auth.AgentRejectUnknown, auth.ErrAgentTokenInvalid)
		}
		return Identity{}, err
	}
	if tok.RevokedAt.Valid {
		return fail(auth.AgentRejectRevoked, auth.ErrAgentTokenRevoked)
	}
	if !tok.ExpiresAt.Valid || !tok.ExpiresAt.Time.After(time.Now()) {
		return fail(auth.AgentRejectExpired, auth.ErrAgentTokenExpired)
	}

	agent, err := q.GetAgentByID(ctx, tok.AgentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(auth.AgentRejectUnknown, auth.ErrAgentTokenInvalid)
		}
		return Identity{}, err
	}
	if agent.Status != "active" {
		return fail(auth.AgentRejectAgentDisabled, auth.ErrAgentDisabled)
	}

	owner, err := q.GetUserByID(ctx, agent.OwnerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(auth.AgentRejectOwnerSuspended, auth.ErrAgentOwnerSuspended)
		}
		return Identity{}, err
	}
	if err := auth.RequireActive(owner.Status); err != nil {
		return fail(auth.AgentRejectOwnerSuspended, auth.ErrAgentOwnerSuspended)
	}

	// Best effort; a failed touch must not reject a valid token.
	_ = q.TouchAgentToken(ctx, tok.ID)

	return Identity{AgentID: agent.ID, OwnerID: agent.OwnerID, Kind: agent.Kind}, nil
}
