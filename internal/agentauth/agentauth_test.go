package agentauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func uid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

type fakeStore struct {
	token db.AgentToken
	agent db.Agent
	owner db.User
}

func (s fakeStore) GetAgentTokenByHash(_ context.Context, hash string) (db.AgentToken, error) {
	if hash != s.token.TokenHash {
		return db.AgentToken{}, pgx.ErrNoRows
	}
	return s.token, nil
}

func (s fakeStore) GetAgentByID(_ context.Context, id pgtype.UUID) (db.Agent, error) {
	if id.Bytes != s.agent.ID.Bytes {
		return db.Agent{}, pgx.ErrNoRows
	}
	return s.agent, nil
}

func (s fakeStore) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	if id.Bytes != s.owner.ID.Bytes {
		return db.User{}, pgx.ErrNoRows
	}
	return s.owner, nil
}

func (s fakeStore) TouchAgentToken(context.Context, pgtype.UUID) error { return nil }

func fixture() (fakeStore, string) {
	raw := auth.AgentTokenPrefix + "test-secret"
	store := fakeStore{
		token: db.AgentToken{
			ID:        uid(1),
			AgentID:   uid(2),
			TokenHash: auth.AgentTokenHash(raw),
			Prefix:    auth.AgentTokenDisplayPrefix(raw),
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		},
		agent: db.Agent{ID: uid(2), OwnerID: uid(3), Kind: "external", Status: "active"},
		owner: db.User{ID: uid(3), Status: auth.StatusActive, Role: "member"},
	}
	return store, raw
}

func TestAuthenticate_Valid(t *testing.T) {
	store, raw := fixture()
	id, err := Authenticate(context.Background(), store, raw)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.AgentID.Bytes != store.agent.ID.Bytes {
		t.Errorf("agent id = %v, want %v", id.AgentID, store.agent.ID)
	}
	if id.OwnerID.Bytes != store.owner.ID.Bytes {
		t.Errorf("owner id = %v, want %v", id.OwnerID, store.owner.ID)
	}
	if id.Kind != "external" {
		t.Errorf("kind = %q, want external", id.Kind)
	}
}

func TestAuthenticate_Rejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeStore)
		raw    func(string) string
		reason string
	}{
		{
			name:   "wrong prefix",
			raw:    func(string) string { return "bal_wrong" },
			reason: auth.AgentRejectUnknown,
		},
		{
			name:   "unknown token",
			raw:    func(string) string { return auth.AgentTokenPrefix + "not-in-the-database" },
			reason: auth.AgentRejectUnknown,
		},
		{
			name: "expired",
			mutate: func(s *fakeStore) {
				s.token.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}
			},
			reason: auth.AgentRejectExpired,
		},
		{
			name: "revoked",
			mutate: func(s *fakeStore) {
				s.token.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			},
			reason: auth.AgentRejectRevoked,
		},
		{
			name: "agent disabled",
			mutate: func(s *fakeStore) {
				s.agent.Status = "disabled"
			},
			reason: auth.AgentRejectAgentDisabled,
		},
		{
			name: "owner suspended",
			mutate: func(s *fakeStore) {
				s.owner.Status = auth.StatusSuspended
			},
			reason: auth.AgentRejectOwnerSuspended,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, raw := fixture()
			if tc.mutate != nil {
				tc.mutate(&store)
			}
			if tc.raw != nil {
				raw = tc.raw(raw)
			}
			_, err := Authenticate(context.Background(), store, raw)
			if err == nil {
				t.Fatal("Authenticate succeeded, want rejection")
			}
			var ae *AuthError
			if !errors.As(err, &ae) {
				t.Fatalf("error = %T %v, want *AuthError", err, err)
			}
			if ae.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", ae.Reason, tc.reason)
			}
		})
	}
}
