// Package providers manages the OIDC and LDAP identity providers (#280,
// #286). Each provider's configuration is sealed with its own id as AAD; the
// caller builds and seals it, this package owns the provider's lifecycle.
package providers

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads providers and updates one; *db.Queries implements it.
type Store interface {
	ListAllAuthProviders(ctx context.Context) ([]db.AuthProvider, error)
	ListAuthProviders(ctx context.Context) ([]db.AuthProvider, error)
	GetAuthProviderByID(ctx context.Context, id pgtype.UUID) (db.AuthProvider, error)
	CountUsersByAuthProvider(ctx context.Context, authProviderID pgtype.UUID) (int64, error)
	UpdateAuthProvider(ctx context.Context, arg db.UpdateAuthProviderParams) (db.AuthProvider, error)
}

// Creator writes a new provider; *db.Queries implements it.
type Creator interface {
	CreateAuthProvider(ctx context.Context, arg db.CreateAuthProviderParams) (db.AuthProvider, error)
	UpdateAuthProvider(ctx context.Context, arg db.UpdateAuthProviderParams) (db.AuthProvider, error)
}

// Remover detaches a provider's users and deletes it; *db.Queries implements it.
type Remover interface {
	RevokeRefreshTokensByAuthProvider(ctx context.Context, authProviderID pgtype.UUID) error
	DeleteAPIKeysByAuthProvider(ctx context.Context, authProviderID pgtype.UUID) error
	SuspendUsersByAuthProvider(ctx context.Context, authProviderID pgtype.UUID) error
	DeleteAuthProvider(ctx context.Context, id pgtype.UUID) error
}

// TxStore is the transactional part.
type TxStore interface {
	Creator
	Remover
}

// Tx runs fn in one transaction over a TxStore.
type Tx func(ctx context.Context, fn func(TxStore) error) error

// Seal seals a provider's configuration for the provider id.
type Seal func(id pgtype.UUID) (string, error)

var (
	// ErrNotFound: no provider has this id.
	ErrNotFound = errors.New("providers: not found")
	// ErrInUse: users are still linked to the provider.
	ErrInUse = errors.New("providers: users are still linked")
)

// Service manages identity providers.
type Service struct {
	store Store
	tx    Tx
}

// New builds the service.
func New(store Store, tx Tx) *Service { return &Service{store: store, tx: tx} }

// ListEnabled returns the enabled providers, for the public login page.
func (s *Service) ListEnabled(ctx context.Context) ([]db.AuthProvider, error) {
	return s.store.ListAuthProviders(ctx)
}

// List returns every provider.
func (s *Service) List(ctx context.Context) ([]db.AuthProvider, error) {
	return s.store.ListAllAuthProviders(ctx)
}

// Get returns one provider.
func (s *Service) Get(ctx context.Context, id pgtype.UUID) (db.AuthProvider, error) {
	ap, err := s.store.GetAuthProviderByID(ctx, id)
	if err != nil {
		return db.AuthProvider{}, ErrNotFound
	}
	return ap, nil
}

// LinkedUsers counts the accounts linked to a provider.
func (s *Service) LinkedUsers(ctx context.Context, id pgtype.UUID) (int64, error) {
	return s.store.CountUsersByAuthProvider(ctx, id)
}

// Create adds a provider. The database generates the id, so the row is
// created first and its configuration sealed with that id in the same
// transaction.
func (s *Service) Create(ctx context.Context, name, typ string, enabled bool, seal Seal) (db.AuthProvider, error) {
	var ap db.AuthProvider
	err := s.tx(ctx, func(q TxStore) error {
		created, err := q.CreateAuthProvider(ctx, db.CreateAuthProviderParams{Name: name, Type: typ, ConfigEnc: "", Enabled: enabled})
		if err != nil {
			return err
		}
		sealed, err := seal(created.ID)
		if err != nil {
			return err
		}
		ap, err = q.UpdateAuthProvider(ctx, db.UpdateAuthProviderParams{ID: created.ID, Name: name, ConfigEnc: sealed, Enabled: enabled})
		return err
	})
	if err != nil {
		return db.AuthProvider{}, err
	}
	return ap, nil
}

// Update stores a provider's name, switch and sealed configuration.
func (s *Service) Update(ctx context.Context, id pgtype.UUID, name string, enabled bool, sealed string) (db.AuthProvider, error) {
	return s.store.UpdateAuthProvider(ctx, db.UpdateAuthProviderParams{ID: id, Name: name, ConfigEnc: sealed, Enabled: enabled})
}

// Delete removes a provider. With linked users it refuses with ErrInUse
// unless suspendUsers, which in the same transaction ends their sessions,
// deletes their API keys and suspends them.
func (s *Service) Delete(ctx context.Context, id pgtype.UUID, suspendUsers bool) error {
	ap, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	linked, err := s.store.CountUsersByAuthProvider(ctx, ap.ID)
	if err != nil {
		return err
	}
	if linked > 0 && !suspendUsers {
		return ErrInUse
	}
	return s.tx(ctx, func(q TxStore) error {
		if linked > 0 {
			if err := q.RevokeRefreshTokensByAuthProvider(ctx, ap.ID); err != nil {
				return err
			}
			if err := q.DeleteAPIKeysByAuthProvider(ctx, ap.ID); err != nil {
				return err
			}
			if err := q.SuspendUsersByAuthProvider(ctx, ap.ID); err != nil {
				return err
			}
		}
		return q.DeleteAuthProvider(ctx, ap.ID)
	})
}
