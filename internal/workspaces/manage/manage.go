// Package manage stores workspaces and their catalog grants (#33, #36):
// listing a user's workspaces, creating, updating with the default target,
// deleting with the personal and running-execution rules, granting and
// revoking catalogs, and the impact of a delete or a revoke. The caller
// checks access and resolves the default target.
package manage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader reads workspaces, grants and what depends on them; *db.Queries
// implements it.
type Reader interface {
	ListWorkspacesByUser(ctx context.Context, ownerUserID pgtype.UUID) ([]db.Workspace, error)
	GetWorkspaceByID(ctx context.Context, id pgtype.UUID) (db.Workspace, error)
	ListWorkspaceCatalogGrants(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListWorkspaceCatalogGrantsRow, error)
	CountWorkspaceRunningExecutions(ctx context.Context, workspaceID pgtype.UUID) (int64, error)
	CountWorkspaceImpact(ctx context.Context, workspaceID pgtype.UUID) (db.CountWorkspaceImpactRow, error)
	CountCatalogAccessImpact(ctx context.Context, arg db.CountCatalogAccessImpactParams) (db.CountCatalogAccessImpactRow, error)
}

// Writer changes workspaces and grants outside a transaction; *db.Queries
// implements it.
type Writer interface {
	CreateWorkspace(ctx context.Context, arg db.CreateWorkspaceParams) (db.Workspace, error)
	DeleteWorkspace(ctx context.Context, id pgtype.UUID) (int64, error)
	UpsertWorkspaceCatalogGrant(ctx context.Context, arg db.UpsertWorkspaceCatalogGrantParams) (db.WorkspaceCatalogGrant, error)
}

// Store is Reader and Writer.
type Store interface {
	Reader
	Writer
}

// TxStore changes a workspace with its default target, or revokes a grant
// with a default on its catalog; db.Querier implements it.
type TxStore interface {
	UpdateWorkspace(ctx context.Context, arg db.UpdateWorkspaceParams) (db.Workspace, error)
	SetWorkspaceDefaultTarget(ctx context.Context, arg db.SetWorkspaceDefaultTargetParams) error
	DeleteWorkspaceCatalogGrant(ctx context.Context, arg db.DeleteWorkspaceCatalogGrantParams) (int64, error)
	ClearWorkspaceDefaultForCatalog(ctx context.Context, arg db.ClearWorkspaceDefaultForCatalogParams) error
}

// Tx runs fn in one transaction.
type Tx func(ctx context.Context, fn func(TxStore) error) error

var (
	// ErrNotFound: no workspace has the id.
	ErrNotFound = errors.New("workspace not found")
	// ErrPersonal: personal workspaces cannot be deleted.
	ErrPersonal = errors.New("personal workspaces cannot be deleted")
	// ErrExecutionsRunning: the workspace has running executions.
	ErrExecutionsRunning = errors.New("wait for the workspace's running executions to finish")
	// ErrInvalidPermission: a grant is read or write.
	ErrInvalidPermission = errors.New("permission must be read or write")
	// ErrGrantNotFound: the catalog is not granted to the workspace.
	ErrGrantNotFound = errors.New("grant not found")
)

// Service manages workspaces.
type Service struct {
	store Store
	tx    Tx
}

// New builds the service.
func New(store Store, tx Tx) *Service { return &Service{store: store, tx: tx} }

// ListForUser returns the workspaces userID belongs to.
func (s *Service) ListForUser(ctx context.Context, userID pgtype.UUID) ([]db.Workspace, error) {
	return s.store.ListWorkspacesByUser(ctx, userID)
}

// Get returns a workspace.
func (s *Service) Get(ctx context.Context, id pgtype.UUID) (db.Workspace, error) {
	w, err := s.store.GetWorkspaceByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Workspace{}, ErrNotFound
	}
	return w, err
}

// NewWorkspace is a workspace to create: a personal one has OwnerUser, a
// shared one OwnerTeam.
type NewWorkspace struct {
	Name        string
	Description *string
	Type        string
	OwnerUser   pgtype.UUID
	OwnerTeam   pgtype.UUID
}

// Create stores a workspace, with a Polaris catalog name derived from its
// name.
func (s *Service) Create(ctx context.Context, n NewWorkspace) (db.Workspace, error) {
	return s.store.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:               n.Name,
		Description:        n.Description,
		Type:               n.Type,
		PolarisCatalogName: PolarisName(n.Name),
		OwnerUserID:        n.OwnerUser,
		OwnerTeamID:        n.OwnerTeam,
	})
}

// Default changes a workspace's default target: a valid Binding sets it,
// an invalid one clears it.
type Default struct {
	Binding pgtype.UUID
	Schema  *string
}

// Edit changes a workspace. Nil fields keep their value.
type Edit struct {
	Name        *string
	Description *string
	Default     *Default
}

// Update applies e to current, with its default target, in one transaction.
func (s *Service) Update(ctx context.Context, current db.Workspace, e Edit) (db.Workspace, error) {
	name, desc := current.Name, current.Description
	if e.Name != nil {
		name = *e.Name
	}
	if e.Description != nil {
		desc = e.Description
	}
	var workspace db.Workspace
	err := s.tx(ctx, func(q TxStore) error {
		var err error
		workspace, err = q.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{ID: current.ID, Name: name, Description: desc})
		if err != nil || e.Default == nil {
			return err
		}
		d := e.Default
		if err := q.SetWorkspaceDefaultTarget(ctx, db.SetWorkspaceDefaultTargetParams{ID: current.ID, DefaultBindingID: d.Binding, DefaultSchema: d.Schema}); err != nil {
			return err
		}
		workspace.DefaultBindingID, workspace.DefaultSchema = d.Binding, d.Schema
		return nil
	})
	if err != nil {
		return db.Workspace{}, err
	}
	return workspace, nil
}

// Delete removes a shared workspace without running executions.
func (s *Service) Delete(ctx context.Context, ws db.Workspace) error {
	if ws.Type == "personal" {
		return ErrPersonal
	}
	running, err := s.store.CountWorkspaceRunningExecutions(ctx, ws.ID)
	if err != nil {
		return err
	}
	if running > 0 {
		return ErrExecutionsRunning
	}
	n, err := s.store.DeleteWorkspace(ctx, ws.ID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Grants lists the catalogs granted to a workspace.
func (s *Service) Grants(ctx context.Context, ws pgtype.UUID) ([]db.ListWorkspaceCatalogGrantsRow, error) {
	return s.store.ListWorkspaceCatalogGrants(ctx, ws)
}

// Grant grants a catalog to a workspace, or changes the permission of an
// existing grant.
func (s *Service) Grant(ctx context.Context, ws, catalogID pgtype.UUID, permission string, by pgtype.UUID) (db.WorkspaceCatalogGrant, error) {
	if permission != "read" && permission != "write" {
		return db.WorkspaceCatalogGrant{}, ErrInvalidPermission
	}
	return s.store.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{WorkspaceID: ws, CatalogID: catalogID, Permission: permission, GrantedBy: by})
}

// Revoke removes a grant and, in the same transaction, a default target on
// its catalog.
func (s *Service) Revoke(ctx context.Context, ws, catalogID pgtype.UUID) error {
	var n int64
	err := s.tx(ctx, func(q TxStore) error {
		var err error
		if n, err = q.DeleteWorkspaceCatalogGrant(ctx, db.DeleteWorkspaceCatalogGrantParams{WorkspaceID: ws, CatalogID: catalogID}); err != nil || n == 0 {
			return err
		}
		return q.ClearWorkspaceDefaultForCatalog(ctx, db.ClearWorkspaceDefaultForCatalogParams{WorkspaceID: ws, CatalogID: catalogID})
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrGrantNotFound
	}
	return nil
}

// Impact counts what deleting a workspace affects.
func (s *Service) Impact(ctx context.Context, ws pgtype.UUID) (db.CountWorkspaceImpactRow, error) {
	return s.store.CountWorkspaceImpact(ctx, ws)
}

// CatalogAccessImpact counts what revoking a catalog from a workspace
// affects.
func (s *Service) CatalogAccessImpact(ctx context.Context, ws, catalogID pgtype.UUID) (db.CountCatalogAccessImpactRow, error) {
	return s.store.CountCatalogAccessImpact(ctx, db.CountCatalogAccessImpactParams{WorkspaceID: ws, CatalogID: catalogID})
}

// PolarisName derives a Polaris catalog name from a workspace name: letters
// and digits lowercased, anything else an underscore, and a short suffix.
func PolarisName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String() + fmt.Sprintf("_%d", time.Now().UnixMilli()%10000)
}
