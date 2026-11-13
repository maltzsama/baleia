// Package conns stores catalog connections (#360): it lists the ones a user
// may see, creates, updates and deletes them. The caller checks access and
// builds the stored columns, sealing the credentials with the keyring.
package conns

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
)

// Store reads and writes connections, and lists engines to tell which
// connections back one; *repo.Repo implements it.
type Store interface {
	ListVisibleCatalogConnections(ctx context.Context, arg db.ListVisibleCatalogConnectionsParams) ([]db.CatalogConnection, error)
	CreateCatalogConnection(ctx context.Context, arg db.CreateCatalogConnectionParams) (db.CatalogConnection, error)
	UpdateCatalogConnection(ctx context.Context, arg db.UpdateCatalogConnectionParams) (db.CatalogConnection, error)
	DeleteCatalogConnection(ctx context.Context, id db.UUID) error
}

var (
	// ErrNameTaken: another connection has the name.
	ErrNameTaken = errors.New("a connection with this name already exists")
	// ErrBacksEngine: an engine runs on the connection.
	ErrBacksEngine = errors.New("an engine runs on this connection; delete the engine first")
)

// Service manages connections.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// List returns every connection to an admin, and to anyone else the ones
// they may see.
func (s *Service) List(ctx context.Context, userID db.UUID, admin bool) ([]db.CatalogConnection, error) {
	return s.store.ListVisibleCatalogConnections(ctx, db.ListVisibleCatalogConnectionsParams{IsAdmin: admin, UserID: userID})
}

// Create stores a connection.
func (s *Service) Create(ctx context.Context, arg db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	c, err := s.store.CreateCatalogConnection(ctx, arg)
	if isUniqueViolation(err) {
		return db.CatalogConnection{}, ErrNameTaken
	}
	return c, err
}

// Update changes a connection; nil fields keep their value. A name another
// connection has is ErrNameTaken.
func (s *Service) Update(ctx context.Context, arg db.UpdateCatalogConnectionParams) (db.CatalogConnection, error) {
	c, err := s.store.UpdateCatalogConnection(ctx, arg)
	if isUniqueViolation(err) {
		return db.CatalogConnection{}, ErrNameTaken
	}
	return c, err
}

// isUniqueViolation reports a unique index violation; the name is the only
// unique column a write can collide on.
func isUniqueViolation(err error) bool {
	pgErr, ok := db.AsPGError(err)
	return ok && pgErr.Code == "23505"
}

// Delete removes a connection and, by cascade, its catalogs. A connection
// an engine runs on is ErrBacksEngine.
func (s *Service) Delete(ctx context.Context, id db.UUID) error {
	err := s.store.DeleteCatalogConnection(ctx, id)
	pgErr, ok := db.AsPGError(err)
	if ok && manage.IsFKViolation(err) && pgErr.ConstraintName == "engines_connection_id_fkey" {
		return ErrBacksEngine
	}
	return err
}
