// Package authz holds the one rule set that decides who may touch connections
// and catalogs. Handlers call it first, before any other database or Trino
// work, and map its two errors to responses:
//
//	ErrNotFound  -> 404: the resource may exist but the caller cannot see it
//	               (no existence oracle).
//	ErrForbidden -> 403: the caller can see it but may not do this to it.
//
// The matrix is the issue-#16 default (D6). A connection is reachable by its
// owner and by a global admin only. A catalog is also readable by members of a
// workspace it has been granted to (workspace_catalog_grants); that is the only
// way a grant widens access, and it never widens writes.
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	// ErrUnauthenticated means the request carries no usable identity.
	ErrUnauthenticated = errors.New("unauthenticated")
)

// Action is what the caller wants to do to the resource.
type Action int

const (
	ActRead Action = iota
	ActWrite
	ActAdmin
)

// RoleAdmin is users.role for a global administrator.
const RoleAdmin = "admin"

// Kind distinguishes a human user from an agent acting for one (#59). An agent
// principal never carries the owner's global role: Role is always member.
type Kind int

const (
	KindUser Kind = iota
	KindAgent
	KindAssistant
)

// Principal is the authenticated caller.
type Principal struct {
	UserID  db.UUID
	Role    string
	Kind    Kind
	AgentID db.UUID
}

// IsAdmin reports whether the caller is a global administrator.
func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

// IsAgent reports whether the caller is an agent (external or assistant).
func (p Principal) IsAgent() bool { return p.Kind != KindUser }

// PrincipalFromContext builds the caller from the request context populated by
// the auth middleware.
func PrincipalFromContext(ctx context.Context) (Principal, error) {
	var id db.UUID
	if err := id.Scan(auth.UserIDFromContext(ctx)); err != nil || !id.Valid {
		return Principal{}, ErrUnauthenticated
	}
	p := Principal{UserID: id, Role: auth.RoleFromContext(ctx)}
	if auth.ActorKindFromContext(ctx) == auth.ActorAgent {
		p.Kind = KindAgent
		var agentID db.UUID
		if err := agentID.Scan(auth.AgentIDFromContext(ctx)); err == nil && agentID.Valid {
			p.AgentID = agentID
		}
	}
	return p, nil
}

// Store is the slice of the database the checker reads.
type Store interface {
	GetCatalogConnectionByID(ctx context.Context, id db.UUID) (db.CatalogConnection, error)
	GetCatalogByID(ctx context.Context, id db.UUID) (db.Catalog, error)
	GetWorkspaceByID(ctx context.Context, id db.UUID) (db.Workspace, error)
	CheckWorkspaceAccess(ctx context.Context, arg db.CheckWorkspaceAccessParams) (bool, error)
	IsWorkspaceOwner(ctx context.Context, arg db.IsWorkspaceOwnerParams) (bool, error)
	GetWorkspaceMemberRole(ctx context.Context, arg db.GetWorkspaceMemberRoleParams) (string, error)
	GetTeamByID(ctx context.Context, id db.UUID) (db.Team, error)
	GetTeamMemberRole(ctx context.Context, arg db.GetTeamMemberRoleParams) (string, error)
	GetEngineByID(ctx context.Context, id db.UUID) (db.Engine, error)
	GetEffectiveSpaceRole(ctx context.Context, arg db.GetEffectiveSpaceRoleParams) (string, error)
	AgentWorkspaceBound(ctx context.Context, arg db.AgentWorkspaceBoundParams) (bool, error)
}

// Checker evaluates the rules against a Store.
type Checker struct {
	q      Store
	denied func(resource, reason string)
	onDeny func(ctx context.Context, resource, id, reason string)
}

// New returns a Checker reading from q.
func New(q Store) *Checker { return &Checker{q: q} }

// WithDenials reports every refusal as (resource, "not_found" | "forbidden"),
// e.g. to metrics.Authz (#87). It returns c.
func (c *Checker) WithDenials(fn func(resource, reason string)) *Checker {
	c.denied = fn
	return c
}

// OnDeny reports every refusal once, with the request context and the
// resource id, e.g. to the audit trail (#43). It returns c.
func (c *Checker) OnDeny(fn func(ctx context.Context, resource, id, reason string)) *Checker {
	c.onDeny = fn
	return c
}

// observe reports a refusal through the denial hooks.
func (c *Checker) observe(ctx context.Context, resource string, id db.UUID, err *error) {
	if *err == nil {
		return
	}
	reason := ""
	switch {
	case errors.Is(*err, ErrNotFound):
		reason = "not_found"
	case errors.Is(*err, ErrForbidden):
		reason = "forbidden"
	default:
		return
	}
	if c.denied != nil {
		c.denied(resource, reason)
	}
	if c.onDeny != nil {
		c.onDeny(ctx, resource, uuidString(id), reason)
	}
}

func uuidString(id db.UUID) string {
	if !id.Valid {
		return ""
	}
	b := id.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Connection loads the connection and checks the caller may perform act on it.
// Today every action requires being the connection's owner or a global admin;
// a missing created_by never matches an owner. Anyone else gets ErrNotFound.
func (c *Checker) Connection(ctx context.Context, p Principal, connID db.UUID, act Action) (_ db.CatalogConnection, err error) {
	defer c.observe(ctx, "connection", connID, &err)
	// Agents never touch connections directly: their data path is the metadata
	// and execution APIs, scoped through bound workspaces (#59).
	if p.IsAgent() {
		return db.CatalogConnection{}, ErrNotFound
	}
	conn, err := c.q.GetCatalogConnectionByID(ctx, connID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return db.CatalogConnection{}, ErrNotFound
		}
		return db.CatalogConnection{}, err
	}
	if p.IsAdmin() || isOwner(p, conn.CreatedBy) {
		return conn, nil
	}
	return db.CatalogConnection{}, ErrNotFound
}

// Catalog loads the catalog and checks the caller on its connection. It also
// returns the connection so handlers do not fetch it twice.
//
// This is the admin plane (ADR 0018 §7.1): only the connection owner and a
// global admin may act on a catalog. Members and agents reach catalogs only
// through their workspace's scope (the data plane), never here, so anyone else
// gets ErrNotFound.
func (c *Checker) Catalog(ctx context.Context, p Principal, catalogID db.UUID, act Action) (_ db.Catalog, _ db.CatalogConnection, err error) {
	defer c.observe(ctx, "catalog", catalogID, &err)
	// Agents are not on the admin plane: their data path is gated by the agent
	// policy, so their owner's connection ownership must not widen this.
	if p.IsAgent() {
		return db.Catalog{}, db.CatalogConnection{}, ErrNotFound
	}
	cat, err := c.q.GetCatalogByID(ctx, catalogID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return db.Catalog{}, db.CatalogConnection{}, ErrNotFound
		}
		return db.Catalog{}, db.CatalogConnection{}, err
	}
	conn, err := c.q.GetCatalogConnectionByID(ctx, cat.ConnectionID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return db.Catalog{}, db.CatalogConnection{}, ErrNotFound
		}
		return db.Catalog{}, db.CatalogConnection{}, err
	}
	if p.IsAdmin() || isOwner(p, conn.CreatedBy) {
		return cat, conn, nil
	}
	return db.Catalog{}, db.CatalogConnection{}, ErrNotFound
}

// Engine loads the engine and checks the caller on it. It is the admin plane:
// only the owner of the engine's connection and a global admin may act; anyone
// else gets ErrNotFound (members reach engines through their workspace scope).
func (c *Checker) Engine(ctx context.Context, p Principal, engineID db.UUID, act Action) (_ db.Engine, _ db.CatalogConnection, err error) {
	defer c.observe(ctx, "engine", engineID, &err)
	// Agents are not on the admin plane: their data path is gated by the agent
	// policy, so their owner's connection ownership must not widen this.
	if p.IsAgent() {
		return db.Engine{}, db.CatalogConnection{}, ErrNotFound
	}
	eng, err := c.q.GetEngineByID(ctx, engineID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return db.Engine{}, db.CatalogConnection{}, ErrNotFound
		}
		return db.Engine{}, db.CatalogConnection{}, err
	}
	conn, err := c.q.GetCatalogConnectionByID(ctx, eng.ConnectionID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return db.Engine{}, db.CatalogConnection{}, ErrNotFound
		}
		return db.Engine{}, db.CatalogConnection{}, err
	}
	if p.IsAdmin() || isOwner(p, conn.CreatedBy) {
		return eng, conn, nil
	}
	return db.Engine{}, db.CatalogConnection{}, ErrNotFound
}

// CatalogOfConnection is Catalog for routes addressed as
// /connections/{connection_id}/catalogs/{catalog_id}: a catalog that does not
// belong to the connection in the path is ErrNotFound, so a valid connection id
// cannot be used to reach someone else's catalog.
func (c *Checker) CatalogOfConnection(ctx context.Context, p Principal, connID, catalogID db.UUID, act Action) (db.Catalog, db.CatalogConnection, error) {
	cat, conn, err := c.Catalog(ctx, p, catalogID, act)
	if err != nil {
		return db.Catalog{}, db.CatalogConnection{}, err
	}
	if cat.ConnectionID.Bytes != connID.Bytes {
		err := ErrNotFound
		c.observe(ctx, "catalog", catalogID, &err) // Catalog itself allowed it
		return db.Catalog{}, db.CatalogConnection{}, err
	}
	return cat, conn, nil
}

// Workspace loads the workspace and checks the caller on it. A global admin may
// do anything; a member may read; managing it (ActWrite, ActAdmin) needs the
// workspace admin role (#653). A non-member gets ErrNotFound, a member below
// admin gets ErrForbidden.
func (c *Checker) Workspace(ctx context.Context, p Principal, wsID db.UUID, act Action) (_ db.Workspace, err error) {
	defer c.observe(ctx, "workspace", wsID, &err)
	if p.IsAgent() && act != ActRead {
		return db.Workspace{}, ErrForbidden
	}
	ws, err := c.q.GetWorkspaceByID(ctx, wsID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return db.Workspace{}, ErrNotFound
		}
		return db.Workspace{}, err
	}
	if p.IsAgent() {
		bound, err := c.q.AgentWorkspaceBound(ctx, db.AgentWorkspaceBoundParams{AgentID: p.AgentID, WorkspaceID: wsID})
		if err != nil {
			return db.Workspace{}, err
		}
		if !bound {
			return db.Workspace{}, ErrNotFound
		}
	}
	if p.IsAdmin() {
		return ws, nil
	}
	member, err := c.q.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{ID: wsID, UserID: p.UserID})
	if err != nil {
		return db.Workspace{}, err
	}
	if !member {
		return db.Workspace{}, ErrNotFound
	}
	if act == ActRead {
		return ws, nil
	}
	owner, err := c.q.IsWorkspaceOwner(ctx, db.IsWorkspaceOwnerParams{ID: wsID, UserID: p.UserID})
	if err != nil {
		return db.Workspace{}, err
	}
	if !owner {
		return db.Workspace{}, ErrForbidden
	}
	return ws, nil
}

// Team loads the team and checks the caller on it. A global admin may do
// anything; a member may read; changing it needs a team owner. A non-member
// gets ErrNotFound, a member who is not an owner gets ErrForbidden.
func (c *Checker) Team(ctx context.Context, p Principal, teamID db.UUID, act Action) (_ db.Team, err error) {
	defer c.observe(ctx, "team", teamID, &err)
	if p.IsAgent() && act != ActRead {
		return db.Team{}, ErrForbidden
	}
	team, err := c.q.GetTeamByID(ctx, teamID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return db.Team{}, ErrNotFound
		}
		return db.Team{}, err
	}
	if p.IsAdmin() {
		return team, nil
	}
	role, err := c.q.GetTeamMemberRole(ctx, db.GetTeamMemberRoleParams{TeamID: teamID, UserID: p.UserID})
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return db.Team{}, ErrNotFound
		}
		return db.Team{}, err
	}
	if act != ActRead && role != "owner" {
		return db.Team{}, ErrForbidden
	}
	return team, nil
}

// Admin requires a global administrator (e.g. Trino reconciliation).
func (p Principal) RequireAdmin() error {
	if p.IsAdmin() {
		return nil
	}
	return ErrForbidden
}

func isOwner(p Principal, createdBy db.UUID) bool {
	return createdBy.Valid && p.UserID.Valid && createdBy.Bytes == p.UserID.Bytes
}
