package manage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	agents   map[[16]byte]db.Agent
	bound    map[[16]byte][]pgtype.UUID
	visible  map[[16]byte]bool
	tokens   map[[16]byte]db.AgentToken
	revoked  []pgtype.UUID
	bindFail error
}

func newStore() *fakeStore {
	return &fakeStore{agents: map[[16]byte]db.Agent{}, bound: map[[16]byte][]pgtype.UUID{}, visible: map[[16]byte]bool{}, tokens: map[[16]byte]db.AgentToken{}}
}

func (f *fakeStore) GetAgentByID(_ context.Context, id pgtype.UUID) (db.Agent, error) {
	if a, ok := f.agents[id.Bytes]; ok {
		return a, nil
	}
	return db.Agent{}, pgx.ErrNoRows
}

func (f *fakeStore) ListAllAgents(context.Context) ([]db.Agent, error) { return nil, nil }

func (f *fakeStore) ListAgentsByOwner(context.Context, pgtype.UUID) ([]db.Agent, error) {
	return nil, nil
}

func (f *fakeStore) ListAgentWorkspaces(context.Context, pgtype.UUID) ([]db.Workspace, error) {
	return nil, nil
}

func (f *fakeStore) ListAgentTokensByAgent(context.Context, pgtype.UUID) ([]db.AgentToken, error) {
	return nil, nil
}

func (f *fakeStore) GetAgentTokenByID(_ context.Context, arg db.GetAgentTokenByIDParams) (db.AgentToken, error) {
	if t, ok := f.tokens[arg.ID.Bytes]; ok && t.AgentID == arg.AgentID {
		return t, nil
	}
	return db.AgentToken{}, pgx.ErrNoRows
}

func (f *fakeStore) CheckWorkspaceAccess(_ context.Context, arg db.CheckWorkspaceAccessParams) (bool, error) {
	return f.visible[arg.ID.Bytes], nil
}

func (f *fakeStore) DeleteAgent(_ context.Context, id pgtype.UUID) error {
	delete(f.agents, id.Bytes)
	return nil
}

func (f *fakeStore) CreateAgentToken(_ context.Context, arg db.CreateAgentTokenParams) (db.AgentToken, error) {
	t := db.AgentToken{ID: newID(), AgentID: arg.AgentID, Name: arg.Name, TokenHash: arg.TokenHash, Prefix: arg.Prefix, ExpiresAt: arg.ExpiresAt}
	f.tokens[t.ID.Bytes] = t
	return t, nil
}

func (f *fakeStore) RevokeAgentToken(_ context.Context, arg db.RevokeAgentTokenParams) error {
	f.revoked = append(f.revoked, arg.ID)
	return nil
}

func (f *fakeStore) CreateAgent(_ context.Context, arg db.CreateAgentParams) (db.Agent, error) {
	a := db.Agent{ID: newID(), OwnerID: arg.OwnerID, Kind: arg.Kind, Name: arg.Name, Status: "active"}
	f.agents[a.ID.Bytes] = a
	return a, nil
}

func (f *fakeStore) UpdateAgent(_ context.Context, arg db.UpdateAgentParams) (db.Agent, error) {
	a := f.agents[arg.ID.Bytes]
	a.Name, a.Description = arg.Name, arg.Description
	f.agents[arg.ID.Bytes] = a
	return a, nil
}

func (f *fakeStore) SetAgentStatus(_ context.Context, arg db.SetAgentStatusParams) (db.Agent, error) {
	a := f.agents[arg.ID.Bytes]
	a.Status = arg.Status
	f.agents[arg.ID.Bytes] = a
	return a, nil
}

func (f *fakeStore) ClearAgentWorkspaces(_ context.Context, id pgtype.UUID) error {
	delete(f.bound, id.Bytes)
	return nil
}

func (f *fakeStore) AddAgentWorkspace(_ context.Context, arg db.AddAgentWorkspaceParams) error {
	if f.bindFail != nil {
		return f.bindFail
	}
	f.bound[arg.AgentID.Bytes] = append(f.bound[arg.AgentID.Bytes], arg.WorkspaceID)
	return nil
}

// tx restores the agents when fn fails.
func (f *fakeStore) tx(_ context.Context, fn func(TxStore) error) error {
	saved := map[[16]byte]db.Agent{}
	for k, v := range f.agents {
		saved[k] = v
	}
	if err := fn(f); err != nil {
		f.agents = saved
		return err
	}
	return nil
}

func TestName(t *testing.T) {
	if n, err := Name("  bot "); err != nil || n != "bot" {
		t.Errorf("Name = %q, %v", n, err)
	}
	for _, bad := range []string{" ", strings.Repeat("x", MaxName+1)} {
		if _, err := Name(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Name(%d bytes) = %v", len(bad), err)
		}
	}
}

func TestVisibility(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	owner, other := newID(), newID()
	a, _ := s.Create(ctx, owner, "bot", "", nil)
	internal := db.Agent{ID: newID(), OwnerID: owner, Kind: "assistant"}
	st.agents[internal.ID.Bytes] = internal

	if _, ok, _ := s.Visible(ctx, Viewer{UserID: owner}, a.ID); !ok {
		t.Error("the owner sees their agent")
	}
	if _, ok, _ := s.Visible(ctx, Viewer{UserID: other}, a.ID); ok {
		t.Error("another member does not see it")
	}
	if _, ok, _ := s.Visible(ctx, Viewer{UserID: other, Admin: true}, a.ID); !ok {
		t.Error("an admin sees it")
	}
	if _, ok, _ := s.Owned(ctx, Viewer{UserID: other, Admin: true}, a.ID); ok {
		t.Error("an admin does not own it")
	}
	if _, ok, _ := s.Visible(ctx, Viewer{UserID: owner}, internal.ID); ok {
		t.Error("only external agents are managed here")
	}
}

func TestWorkspacesAndUpdate(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	owner, ws, hidden := newID(), newID(), newID()
	st.visible[ws.Bytes] = true

	if _, err := s.CheckWorkspaces(ctx, owner, []pgtype.UUID{ws, hidden}); !errors.Is(err, ErrWorkspaceNotVisible) {
		t.Errorf("hidden workspace = %v", err)
	}
	ids, err := s.CheckWorkspaces(ctx, owner, []pgtype.UUID{ws, ws})
	if err != nil || len(ids) != 1 {
		t.Fatalf("dedupe = %v, %v", ids, err)
	}
	a, err := s.Create(ctx, owner, "bot", "", ids)
	if err != nil || len(st.bound[a.ID.Bytes]) != 1 {
		t.Fatalf("Create = %+v, %v; want it bound to the workspace", a, err)
	}

	disabled := "disabled"
	got, err := s.Update(ctx, a, Edit{Name: "bot2", Status: &disabled})
	if err != nil || got.Name != "bot2" || got.Status != "disabled" || len(st.bound[a.ID.Bytes]) != 1 {
		t.Errorf("Update = %+v, %v; want renamed, disabled and still bound", got, err)
	}

	st.bindFail = errors.New("connection reset")
	none := []pgtype.UUID{ws}
	if _, err := s.Update(ctx, got, Edit{Name: "bot3", Workspaces: &none}); !errors.Is(err, st.bindFail) {
		t.Fatalf("Update = %v, want the bind error", err)
	}
	if st.agents[a.ID.Bytes].Name != "bot2" {
		t.Error("a failed update must not rename the agent")
	}
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	day := 24 * time.Hour

	zero, many, three := 0, 400, 3
	if _, err := TokenTTL(&zero, day, 30*day); !errors.Is(err, ErrTTLTooShort) {
		t.Errorf("zero days = %v", err)
	}
	if _, err := TokenTTL(&many, day, 30*day); !errors.Is(err, ErrTTLTooLong) {
		t.Errorf("too many days = %v", err)
	}
	if ttl, _ := TokenTTL(&three, day, 30*day); ttl != 3*day {
		t.Errorf("ttl = %v", ttl)
	}
	if ttl, _ := TokenTTL(nil, day, 30*day); ttl != day {
		t.Errorf("default ttl = %v", ttl)
	}

	agent := newID()
	tok, secret, err := s.IssueToken(ctx, agent, "ci", day, newID())
	if err != nil || secret == "" || tok.TokenHash == secret {
		t.Fatalf("IssueToken = %+v, %q, %v; want a secret that is not the stored hash", tok, secret, err)
	}
	if err := s.RevokeToken(ctx, newID(), tok.ID); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("another agent's token = %v, want ErrTokenNotFound", err)
	}
	if err := s.RevokeToken(ctx, agent, tok.ID); err != nil || len(st.revoked) != 1 {
		t.Errorf("RevokeToken = %v, %d revoked", err, len(st.revoked))
	}
}
