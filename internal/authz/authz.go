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

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

// Principal is the authenticated caller.
type Principal struct {
	UserID pgtype.UUID
	Role   string
}

// IsAdmin reports whether the caller is a global administrator.
func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

// PrincipalFromContext builds the caller from the request context populated by
// the auth middleware.
func PrincipalFromContext(ctx context.Context) (Principal, error) {
	var id pgtype.UUID
	if err := id.Scan(auth.UserIDFromContext(ctx)); err != nil || !id.Valid {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{UserID: id, Role: auth.RoleFromContext(ctx)}, nil
}

// Store is the slice of the database the checker reads.
type Store interface {
	GetCatalogConnectionByID(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error)
	GetCatalogByID(ctx context.Context, id pgtype.UUID) (db.Catalog, error)
	CatalogGrantedToUser(ctx context.Context, arg db.CatalogGrantedToUserParams) (bool, error)
	GetWorkspaceByID(ctx context.Context, id pgtype.UUID) (db.Workspace, error)
	CheckWorkspaceAccess(ctx context.Context, arg db.CheckWorkspaceAccessParams) (bool, error)
	IsWorkspaceOwner(ctx context.Context, arg db.IsWorkspaceOwnerParams) (bool, error)
	GetTeamByID(ctx context.Context, id pgtype.UUID) (db.Team, error)
	GetTeamMemberRole(ctx context.Context, arg db.GetTeamMemberRoleParams) (string, error)
}

// Checker evaluates the rules against a Store.
type Checker struct{ q Store }

// New returns a Checker reading from q.
func New(q Store) *Checker { return &Checker{q: q} }

// Connection loads the connection and checks the caller may perform act on it.
// Today every action requires being the connection's owner or a global admin;
// a missing created_by never matches an owner. Anyone else gets ErrNotFound.
func (c *Checker) Connection(ctx context.Context, p Principal, connID pgtype.UUID, act Action) (db.CatalogConnection, error) {
	conn, err := c.q.GetCatalogConnectionByID(ctx, connID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
// The connection owner and a global admin may do anything. A member of a
// workspace the catalog is granted to may read it (ErrForbidden for anything
// else, since they can already see it). Everyone else gets ErrNotFound.
func (c *Checker) Catalog(ctx context.Context, p Principal, catalogID pgtype.UUID, act Action) (db.Catalog, db.CatalogConnection, error) {
	cat, err := c.q.GetCatalogByID(ctx, catalogID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Catalog{}, db.CatalogConnection{}, ErrNotFound
		}
		return db.Catalog{}, db.CatalogConnection{}, err
	}
	conn, err := c.q.GetCatalogConnectionByID(ctx, cat.ConnectionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Catalog{}, db.CatalogConnection{}, ErrNotFound
		}
		return db.Catalog{}, db.CatalogConnection{}, err
	}
	if p.IsAdmin() || isOwner(p, conn.CreatedBy) {
		return cat, conn, nil
	}
	granted, err := c.q.CatalogGrantedToUser(ctx, db.CatalogGrantedToUserParams{CatalogID: cat.ID, UserID: p.UserID})
	if err != nil {
		return db.Catalog{}, db.CatalogConnection{}, err
	}
	if !granted {
		return db.Catalog{}, db.CatalogConnection{}, ErrNotFound
	}
	if act != ActRead {
		return db.Catalog{}, db.CatalogConnection{}, ErrForbidden
	}
	return cat, conn, nil
}

// CatalogOfConnection is Catalog for routes addressed as
// /connections/{connection_id}/catalogs/{catalog_id}: a catalog that does not
// belong to the connection in the path is ErrNotFound, so a valid connection id
// cannot be used to reach someone else's catalog.
func (c *Checker) CatalogOfConnection(ctx context.Context, p Principal, connID, catalogID pgtype.UUID, act Action) (db.Catalog, db.CatalogConnection, error) {
	cat, conn, err := c.Catalog(ctx, p, catalogID, act)
	if err != nil {
		return db.Catalog{}, db.CatalogConnection{}, err
	}
	if cat.ConnectionID.Bytes != connID.Bytes {
		return db.Catalog{}, db.CatalogConnection{}, ErrNotFound
	}
	return cat, conn, nil
}

// Workspace loads the workspace and checks the caller on it. A global admin may
// do anything; a member may read; changing it needs its owner (the user of a
// personal workspace, an owner of the team of a shared one). A non-member gets
// ErrNotFound, a member who is not an owner gets ErrForbidden.
func (c *Checker) Workspace(ctx context.Context, p Principal, wsID pgtype.UUID, act Action) (db.Workspace, error) {
	ws, err := c.q.GetWorkspaceByID(ctx, wsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Workspace{}, ErrNotFound
		}
		return db.Workspace{}, err
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
func (c *Checker) Team(ctx context.Context, p Principal, teamID pgtype.UUID, act Action) (db.Team, error) {
	team, err := c.q.GetTeamByID(ctx, teamID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Team{}, ErrNotFound
		}
		return db.Team{}, err
	}
	if p.IsAdmin() {
		return team, nil
	}
	role, err := c.q.GetTeamMemberRole(ctx, db.GetTeamMemberRoleParams{TeamID: teamID, UserID: p.UserID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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

func isOwner(p Principal, createdBy pgtype.UUID) bool {
	return createdBy.Valid && p.UserID.Valid && createdBy.Bytes == p.UserID.Bytes
}
