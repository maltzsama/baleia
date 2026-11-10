// Package bikeys issues and validates workspace-bound BI keys (ADR 0017).
//
// A BI key is an API key of kind "bi": one key, one workspace, one engine. The
// gateway authenticates it with HTTP Basic (the owner's e-mail and the key),
// and re-checks membership and the engine's presence in the workspace scope on
// every request. The key never carries a workspace or engine chosen by the
// client.
package bikeys

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/apikeys"
)

// Errors returned to the API and the gateway.
var (
	// ErrInvalid covers a bad key, e-mail mismatch, expiry, lost membership or
	// a target that left the workspace scope. The gateway answers 401 for it.
	ErrInvalid = errors.New("invalid bi key")
	// ErrForbidden covers a member trying to act for someone else.
	ErrForbidden = errors.New("forbidden")
	// ErrNotFound covers a key that does not belong to the workspace.
	ErrNotFound = errors.New("bi key not found")
	// ErrTargetUnavailable covers an engine with no usable binding in scope.
	ErrTargetUnavailable = errors.New("bi target unavailable")
)

// Store is the persistence the service needs; *repo.Repo implements it.
type Store interface {
	CreateAPIKey(ctx context.Context, arg db.CreateAPIKeyParams) (db.ApiKey, error)
	GetAPIKeyByHash(ctx context.Context, keyHash string) (db.ApiKey, error)
	GetUserByID(ctx context.Context, id db.UUID) (db.User, error)
	CheckWorkspaceAccess(ctx context.Context, arg db.CheckWorkspaceAccessParams) (bool, error)
	ListWorkspaceTargets(ctx context.Context, workspaceID db.UUID) ([]db.ListWorkspaceTargetsRow, error)
	ListBIKeysByWorkspace(ctx context.Context, workspaceID db.UUID) ([]db.ApiKey, error)
	ListBIKeysByWorkspaceOwner(ctx context.Context, workspaceID, userID db.UUID) ([]db.ApiKey, error)
	GetBIKeyByID(ctx context.Context, id, workspaceID db.UUID) (db.ApiKey, error)
	DeleteBIKey(ctx context.Context, id, workspaceID db.UUID) (int64, error)
	TouchAPIKey(ctx context.Context, id db.UUID) error
}

// Service issues, lists and revokes BI keys and authenticates a gateway
// request.
type Service struct {
	store Store
	now   func() time.Time
}

// New builds the service.
func New(store Store) *Service { return &Service{store: store, now: time.Now} }

// CreateParams describes a new BI key. OwnerID empty means the actor.
type CreateParams struct {
	WorkspaceID db.UUID
	EngineID    db.UUID
	ActorID     db.UUID
	OwnerID     db.UUID
	// ActorIsWorkspaceAdmin permits OwnerID to name a service account of the
	// workspace. A member may only create a key for themselves.
	ActorIsWorkspaceAdmin bool
	Name                  string
	ExpiresAt             *time.Time
}

// Create issues a BI key and returns it with its secret, shown only once. A
// member creates a key for themselves; a workspace admin may create one for a
// service account that is a member of the workspace.
func (s *Service) Create(ctx context.Context, p CreateParams) (db.ApiKey, string, error) {
	if !p.WorkspaceID.Valid || !p.EngineID.Valid || !p.ActorID.Valid {
		return db.ApiKey{}, "", ErrInvalid
	}
	owner := p.OwnerID
	if !owner.Valid {
		owner = p.ActorID
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return db.ApiKey{}, "", ErrInvalid
	}
	if p.ExpiresAt != nil && !p.ExpiresAt.After(s.now()) {
		return db.ApiKey{}, "", ErrInvalid
	}
	if owner.Bytes != p.ActorID.Bytes {
		if !p.ActorIsWorkspaceAdmin {
			return db.ApiKey{}, "", ErrForbidden
		}
		user, err := s.store.GetUserByID(ctx, owner)
		if err != nil {
			return db.ApiKey{}, "", err
		}
		if user.Kind != "service_account" {
			return db.ApiKey{}, "", ErrForbidden
		}
	}
	member, err := s.store.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{ID: p.WorkspaceID, UserID: owner})
	if err != nil {
		return db.ApiKey{}, "", err
	}
	if !member {
		return db.ApiKey{}, "", ErrForbidden
	}
	if _, err := s.target(ctx, p.WorkspaceID, p.EngineID); err != nil {
		return db.ApiKey{}, "", err
	}
	secret := apikeys.NewSecret()
	key, err := s.store.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:      owner,
		Kind:        "bi",
		WorkspaceID: p.WorkspaceID,
		EngineID:    p.EngineID,
		Name:        name,
		KeyHash:     auth.HashAPIKey(secret),
		Prefix:      secret[:8],
		Scopes:      []string{"bi"},
		ExpiresAt:   expiresAt(p.ExpiresAt),
	})
	if err != nil {
		return db.ApiKey{}, "", err
	}
	return key, secret, nil
}

// Credential is the identity a valid BI request runs as.
type Credential struct {
	Key  db.ApiKey
	User db.User
}

// Authenticate checks an HTTP Basic credential on every request: the key
// hash, its kind and expiry, the owner's e-mail, active state, workspace
// membership and the engine's usable binding in the workspace scope.
func (s *Service) Authenticate(ctx context.Context, email, secret string) (Credential, error) {
	if secret == "" || strings.TrimSpace(email) == "" {
		return Credential{}, ErrInvalid
	}
	key, err := s.store.GetAPIKeyByHash(ctx, auth.HashAPIKey(secret))
	if errors.Is(err, db.ErrNoRows) {
		return Credential{}, ErrInvalid
	}
	if err != nil {
		return Credential{}, err
	}
	if key.Kind != "bi" || !key.WorkspaceID.Valid || !key.EngineID.Valid || !slices.Contains(key.Scopes, "bi") {
		return Credential{}, ErrInvalid
	}
	if key.ExpiresAt.Valid && !key.ExpiresAt.Time.After(s.now()) {
		return Credential{}, ErrInvalid
	}
	user, err := s.store.GetUserByID(ctx, key.UserID)
	if err != nil {
		return Credential{}, ErrInvalid
	}
	if err := auth.RequireActive(user.Status); err != nil {
		return Credential{}, ErrInvalid
	}
	if !strings.EqualFold(strings.TrimSpace(user.Email), strings.TrimSpace(email)) {
		return Credential{}, ErrInvalid
	}
	member, err := s.store.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{ID: key.WorkspaceID, UserID: key.UserID})
	if err != nil {
		return Credential{}, err
	}
	if !member {
		return Credential{}, ErrInvalid
	}
	if _, err := s.target(ctx, key.WorkspaceID, key.EngineID); err != nil {
		return Credential{}, ErrInvalid
	}
	// Best effort: a failed touch never rejects a valid key.
	_ = s.store.TouchAPIKey(ctx, key.ID)
	return Credential{Key: key, User: user}, nil
}

// List returns the workspace's BI keys: all of them for a workspace admin,
// the caller's own for a member.
func (s *Service) List(ctx context.Context, workspaceID, actorID db.UUID, admin bool) ([]db.ApiKey, error) {
	if !workspaceID.Valid || !actorID.Valid {
		return nil, ErrInvalid
	}
	if admin {
		return s.store.ListBIKeysByWorkspace(ctx, workspaceID)
	}
	return s.store.ListBIKeysByWorkspaceOwner(ctx, workspaceID, actorID)
}

// Revoke removes a BI key. The owner may revoke their own key; a workspace
// admin may revoke any key of the workspace.
func (s *Service) Revoke(ctx context.Context, workspaceID, keyID, actorID db.UUID, admin bool) (db.ApiKey, error) {
	if !workspaceID.Valid || !keyID.Valid || !actorID.Valid {
		return db.ApiKey{}, ErrInvalid
	}
	key, err := s.store.GetBIKeyByID(ctx, keyID, workspaceID)
	if errors.Is(err, db.ErrNoRows) {
		return db.ApiKey{}, ErrNotFound
	}
	if err != nil {
		return db.ApiKey{}, err
	}
	if key.UserID.Bytes != actorID.Bytes && !admin {
		return db.ApiKey{}, ErrForbidden
	}
	deleted, err := s.store.DeleteBIKey(ctx, keyID, workspaceID)
	if err != nil {
		return db.ApiKey{}, err
	}
	if deleted == 0 {
		return db.ApiKey{}, ErrNotFound
	}
	return key, nil
}

// target reports the workspace's usable Trino binding on the key's engine.
func (s *Service) target(ctx context.Context, workspaceID, engineID db.UUID) (db.ListWorkspaceTargetsRow, error) {
	rows, err := s.store.ListWorkspaceTargets(ctx, workspaceID)
	if err != nil {
		return db.ListWorkspaceTargetsRow{}, err
	}
	for _, row := range rows {
		if row.EngineID.Bytes != engineID.Bytes || !row.BindingEnabled || !row.EngineEnabled || !row.CatalogEnabled {
			continue
		}
		if row.EngineKind != "trino" {
			continue
		}
		return row, nil
	}
	return db.ListWorkspaceTargetsRow{}, ErrTargetUnavailable
}

func expiresAt(t *time.Time) db.Timestamptz {
	if t == nil {
		return db.Timestamptz{}
	}
	return db.Timestamptz{Time: *t, Valid: true}
}
