// Package manage stores external agents and their tokens (#59): who sees an
// agent, creating one bound to workspaces its owner can use, updating,
// deleting, and issuing and revoking tokens. The caller decides what the
// caller may do with an agent it can see (an admin may only disable or
// delete one).
package manage

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader reads agents, their workspaces and tokens; *db.Queries implements
// it.
type Reader interface {
	GetAgentByID(ctx context.Context, id pgtype.UUID) (db.Agent, error)
	ListAllAgents(ctx context.Context) ([]db.Agent, error)
	ListAgentsByOwner(ctx context.Context, ownerID pgtype.UUID) ([]db.Agent, error)
	ListAgentWorkspaces(ctx context.Context, agentID pgtype.UUID) ([]db.Workspace, error)
	ListAgentTokensByAgent(ctx context.Context, agentID pgtype.UUID) ([]db.AgentToken, error)
	GetAgentTokenByID(ctx context.Context, arg db.GetAgentTokenByIDParams) (db.AgentToken, error)
	CheckWorkspaceAccess(ctx context.Context, arg db.CheckWorkspaceAccessParams) (bool, error)
}

// Reach reads what an agent can reach through its workspaces, and the
// executions it ran; *db.Queries implements it.
type Reach interface {
	ListWorkspaceCatalogGrants(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListWorkspaceCatalogGrantsRow, error)
	ListBindingsByCatalog(ctx context.Context, catalogID pgtype.UUID) ([]db.CatalogBinding, error)
	GetEngineByID(ctx context.Context, id pgtype.UUID) (db.Engine, error)
	GetQueryExecutionByIDAndWorkspace(ctx context.Context, arg db.GetQueryExecutionByIDAndWorkspaceParams) (db.QueryExecution, error)
}

// Writer changes agents and tokens outside a transaction; *db.Queries
// implements it.
type Writer interface {
	DeleteAgent(ctx context.Context, id pgtype.UUID) error
	CreateAgentToken(ctx context.Context, arg db.CreateAgentTokenParams) (db.AgentToken, error)
	RevokeAgentToken(ctx context.Context, arg db.RevokeAgentTokenParams) error
	UpdateAgentPolicy(ctx context.Context, arg db.UpdateAgentPolicyParams) (db.Agent, error)
}

// Store is Reader, Reach and Writer.
type Store interface {
	Reader
	Reach
	Writer
}

// TxStore writes an agent with its workspace bindings; db.Querier implements
// it.
type TxStore interface {
	CreateAgent(ctx context.Context, arg db.CreateAgentParams) (db.Agent, error)
	UpdateAgent(ctx context.Context, arg db.UpdateAgentParams) (db.Agent, error)
	SetAgentStatus(ctx context.Context, arg db.SetAgentStatusParams) (db.Agent, error)
	ClearAgentWorkspaces(ctx context.Context, agentID pgtype.UUID) error
	AddAgentWorkspace(ctx context.Context, arg db.AddAgentWorkspaceParams) error
}

// Tx runs fn in one transaction.
type Tx func(ctx context.Context, fn func(TxStore) error) error

// MaxName is the longest agent or token name, in bytes.
const MaxName = 80

var (
	// ErrInvalidName: a name is 1 to MaxName characters.
	ErrInvalidName = errors.New("name must be 1-80 characters")
	// ErrWorkspaceNotVisible: the owner cannot use a workspace to bind.
	ErrWorkspaceNotVisible = errors.New("workspace not visible")
	// ErrExecutionNotFound: the workspace has no execution with the id.
	ErrExecutionNotFound = errors.New("execution not found")
	// ErrTokenNotFound: the agent has no token with the id.
	ErrTokenNotFound = errors.New("agent token not found")
	// ErrTTLTooShort and ErrTTLTooLong bound a token's lifetime.
	ErrTTLTooShort = errors.New("expires_in_days must be at least 1")
	ErrTTLTooLong  = errors.New("expires_in_days exceeds the server maximum")
)

// Service manages agents.
type Service struct {
	store Store
	tx    Tx
}

// New builds the service.
func New(store Store, tx Tx) *Service { return &Service{store: store, tx: tx} }

// Name trims and checks an agent or token name.
func Name(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > MaxName {
		return "", ErrInvalidName
	}
	return name, nil
}

// Viewer is who looks at agents.
type Viewer struct {
	UserID pgtype.UUID
	Admin  bool
}

// Visible returns an external agent the viewer owns, or any external agent
// to an admin; ok is false otherwise.
func (s *Service) Visible(ctx context.Context, v Viewer, id pgtype.UUID) (db.Agent, bool, error) {
	agent, err := s.store.GetAgentByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Agent{}, false, nil
	}
	if err != nil {
		return db.Agent{}, false, err
	}
	if agent.Kind != "external" || (agent.OwnerID.Bytes != v.UserID.Bytes && !v.Admin) {
		return db.Agent{}, false, nil
	}
	return agent, true, nil
}

// Owned returns an external agent the viewer owns; an admin does not own
// other users' agents.
func (s *Service) Owned(ctx context.Context, v Viewer, id pgtype.UUID) (db.Agent, bool, error) {
	agent, ok, err := s.Visible(ctx, v, id)
	if err != nil || !ok || agent.OwnerID.Bytes != v.UserID.Bytes {
		return db.Agent{}, false, err
	}
	return agent, true, nil
}

// List returns the agents of owner, or every agent when all is set.
func (s *Service) List(ctx context.Context, owner pgtype.UUID, all bool) ([]db.Agent, error) {
	if all {
		return s.store.ListAllAgents(ctx)
	}
	return s.store.ListAgentsByOwner(ctx, owner)
}

// Workspaces lists the workspaces an agent is bound to.
func (s *Service) Workspaces(ctx context.Context, agentID pgtype.UUID) ([]db.Workspace, error) {
	return s.store.ListAgentWorkspaces(ctx, agentID)
}

// Tokens lists an agent's tokens, revoked and expired included.
func (s *Service) Tokens(ctx context.Context, agentID pgtype.UUID) ([]db.AgentToken, error) {
	return s.store.ListAgentTokensByAgent(ctx, agentID)
}

// CheckWorkspaces dedupes ids and checks the owner can use each workspace.
func (s *Service) CheckWorkspaces(ctx context.Context, owner pgtype.UUID, ids []pgtype.UUID) ([]pgtype.UUID, error) {
	out := make([]pgtype.UUID, 0, len(ids))
	seen := make(map[[16]byte]bool, len(ids))
	for _, ws := range ids {
		if seen[ws.Bytes] {
			continue
		}
		seen[ws.Bytes] = true
		ok, err := s.store.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{ID: ws, UserID: owner})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrWorkspaceNotVisible
		}
		out = append(out, ws)
	}
	return out, nil
}

// Create stores an external agent of owner bound to workspaces, already
// checked with CheckWorkspaces.
func (s *Service) Create(ctx context.Context, owner pgtype.UUID, name, description string, workspaces []pgtype.UUID) (db.Agent, error) {
	var agent db.Agent
	err := s.tx(ctx, func(q TxStore) error {
		var err error
		agent, err = q.CreateAgent(ctx, db.CreateAgentParams{OwnerID: owner, Kind: "external", Name: name, Description: description})
		if err != nil {
			return err
		}
		return bind(ctx, q, agent.ID, workspaces)
	})
	if err != nil {
		return db.Agent{}, err
	}
	return agent, nil
}

// Edit changes an agent. Status and Workspaces nil keep their value.
type Edit struct {
	Name        string
	Description string
	Status      *string
	// Workspaces replaces the bindings; already checked.
	Workspaces *[]pgtype.UUID
}

// Update applies e to agent in one transaction and returns it as stored.
func (s *Service) Update(ctx context.Context, agent db.Agent, e Edit) (db.Agent, error) {
	err := s.tx(ctx, func(q TxStore) error {
		if _, err := q.UpdateAgent(ctx, db.UpdateAgentParams{ID: agent.ID, Name: e.Name, Description: e.Description}); err != nil {
			return err
		}
		if e.Status != nil && *e.Status != agent.Status {
			if _, err := q.SetAgentStatus(ctx, db.SetAgentStatusParams{ID: agent.ID, Status: *e.Status}); err != nil {
				return err
			}
		}
		if e.Workspaces != nil {
			return bind(ctx, q, agent.ID, *e.Workspaces)
		}
		return nil
	})
	if err != nil {
		return db.Agent{}, err
	}
	return s.store.GetAgentByID(ctx, agent.ID)
}

// Delete removes an agent and, by cascade, its tokens and bindings.
func (s *Service) Delete(ctx context.Context, id pgtype.UUID) error {
	return s.store.DeleteAgent(ctx, id)
}

// TokenTTL is a token's lifetime: def when days is nil, else days, at least
// one and at most max.
func TokenTTL(days *int, def, maxTTL time.Duration) (time.Duration, error) {
	if days == nil {
		return def, nil
	}
	if *days < 1 {
		return 0, ErrTTLTooShort
	}
	ttl := time.Duration(*days) * 24 * time.Hour
	if ttl > maxTTL {
		return 0, ErrTTLTooLong
	}
	return ttl, nil
}

// IssueToken stores a new token of agentID; secret is shown once and only
// its hash is stored.
func (s *Service) IssueToken(ctx context.Context, agentID pgtype.UUID, name string, ttl time.Duration, by pgtype.UUID) (token db.AgentToken, secret string, err error) {
	secret, hash, prefix := auth.NewAgentToken()
	token, err = s.store.CreateAgentToken(ctx, db.CreateAgentTokenParams{
		AgentID: agentID, Name: name, TokenHash: hash, Prefix: prefix,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true}, CreatedBy: by,
	})
	if err != nil {
		return db.AgentToken{}, "", err
	}
	return token, secret, nil
}

// RevokeToken revokes one of agentID's tokens.
func (s *Service) RevokeToken(ctx context.Context, agentID, tokenID pgtype.UUID) error {
	if _, err := s.store.GetAgentTokenByID(ctx, db.GetAgentTokenByIDParams{ID: tokenID, AgentID: agentID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenNotFound
		}
		return err
	}
	return s.store.RevokeAgentToken(ctx, db.RevokeAgentTokenParams{ID: tokenID, AgentID: agentID})
}

// SetPolicy stores an agent's policy, already validated, as by.
func (s *Service) SetPolicy(ctx context.Context, agentID pgtype.UUID, policy []byte, by pgtype.UUID) (db.Agent, error) {
	return s.store.UpdateAgentPolicy(ctx, db.UpdateAgentPolicyParams{ID: agentID, Policy: policy, PolicyUpdatedBy: by})
}

// EngineReach is an enabled engine bound to a catalog an agent reaches.
type EngineReach struct {
	EngineID pgtype.UUID
	Engine   db.Engine
}

// CatalogReach is a catalog granted to one of an agent's workspaces, with
// the enabled engines it is bound to.
type CatalogReach struct {
	CatalogID   pgtype.UUID
	CatalogName string
	Engines     []EngineReach
}

// Reach lists the catalogs an agent reaches through its workspaces, each
// once, with their enabled bindings on enabled engines.
func (s *Service) Reach(ctx context.Context, agentID pgtype.UUID) ([]CatalogReach, error) {
	workspaces, err := s.store.ListAgentWorkspaces(ctx, agentID)
	if err != nil {
		return nil, err
	}
	var out []CatalogReach
	seen := make(map[[16]byte]bool)
	for _, ws := range workspaces {
		grants, err := s.store.ListWorkspaceCatalogGrants(ctx, ws.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range grants {
			if !g.CatalogID.Valid || seen[g.CatalogID.Bytes] {
				continue
			}
			seen[g.CatalogID.Bytes] = true
			entry := CatalogReach{CatalogID: g.CatalogID, CatalogName: g.CatalogName}
			bindings, err := s.store.ListBindingsByCatalog(ctx, g.CatalogID)
			if err != nil {
				return nil, err
			}
			for _, b := range bindings {
				if !b.Enabled || !b.EngineID.Valid {
					continue
				}
				engine, err := s.store.GetEngineByID(ctx, b.EngineID)
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				if err != nil {
					return nil, err
				}
				if engine.Enabled {
					entry.Engines = append(entry.Engines, EngineReach{EngineID: b.EngineID, Engine: engine})
				}
			}
			out = append(out, entry)
		}
	}
	return out, nil
}

// Execution returns an execution of a workspace, for an agent reading its
// results.
func (s *Service) Execution(ctx context.Context, ws, id pgtype.UUID) (db.QueryExecution, error) {
	e, err := s.store.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{ID: id, WorkspaceID: ws})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.QueryExecution{}, ErrExecutionNotFound
	}
	return e, err
}

func bind(ctx context.Context, q TxStore, agentID pgtype.UUID, workspaces []pgtype.UUID) error {
	if err := q.ClearAgentWorkspaces(ctx, agentID); err != nil {
		return err
	}
	for _, id := range workspaces {
		if err := q.AddAgentWorkspace(ctx, db.AddAgentWorkspaceParams{AgentID: agentID, WorkspaceID: id}); err != nil {
			return err
		}
	}
	return nil
}
