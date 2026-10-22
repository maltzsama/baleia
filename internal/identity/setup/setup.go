// Package setup creates the first administrator of a fresh installation.
package setup

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
)

// Store reads and creates accounts; *db.Queries implements it.
type Store interface {
	HasUsers(ctx context.Context) (bool, error)
	LockFirstAdminSetup(ctx context.Context) error
	CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error)
}

// Tx runs fn in one transaction over a Store.
type Tx func(ctx context.Context, fn func(Store) error) error

// ErrAlreadySetUp: the installation already has an account.
var ErrAlreadySetUp = errors.New("setup: already set up")

// Service creates the first administrator.
type Service struct {
	store        Store
	tx           Tx
	passwordCost int
}

// New builds the service; passwordCost is the bcrypt cost.
func New(store Store, tx Tx, passwordCost int) *Service {
	return &Service{store: store, tx: tx, passwordCost: passwordCost}
}

// Configured reports whether any account exists. It is only a fast path for
// CreateFirstAdmin, which checks again under a lock.
func (s *Service) Configured(ctx context.Context) (bool, error) {
	return s.store.HasUsers(ctx)
}

// CreateFirstAdmin creates the local administrator. Of two concurrent setups
// exactly one wins; the other gets ErrAlreadySetUp (#375).
func (s *Service) CreateFirstAdmin(ctx context.Context, name, email, password string) (db.User, error) {
	hash, err := auth.HashPasswordWithCost(password, s.passwordCost)
	if err != nil {
		return db.User{}, err
	}
	var user db.User
	err = s.tx(ctx, func(q Store) error {
		if err := q.LockFirstAdminSetup(ctx); err != nil {
			return err
		}
		if configured, err := q.HasUsers(ctx); err != nil {
			return err
		} else if configured {
			return ErrAlreadySetUp
		}
		user, err = q.CreateUser(ctx, db.CreateUserParams{
			Name:         name,
			Email:        email,
			PasswordHash: &hash,
			Role:         "admin",
			AuthProvider: "local",
		})
		return err
	})
	if err != nil {
		return db.User{}, err
	}
	return user, nil
}
