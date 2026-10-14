package queryengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the slice of the database the manager reads.
type Store interface {
	GetEngineWithConnection(ctx context.Context, id pgtype.UUID) (db.GetEngineWithConnectionRow, error)
}

// Manager builds engines from `engines` rows on demand and caches them by
// (id, updated_at). A changed row builds a new instance; the old one is closed
// once its open sessions are closed. It is constructed at the wiring sites
// (docs/adr/0007).
type Manager struct {
	store     Store
	keyring   *crypto.Keyring
	providers *connections.Registry
	dialer    *connections.Dialer

	mu    sync.Mutex
	cache map[[16]byte]*managed
}

// NewManager returns a manager; the keyring may be nil when no stored
// connection has credentials.
func NewManager(store Store, keyring *crypto.Keyring, providers *connections.Registry, dialer *connections.Dialer) *Manager {
	return &Manager{store: store, keyring: keyring, providers: providers, dialer: dialer, cache: map[[16]byte]*managed{}}
}

// Get returns the engine of an `engines` row.
func (m *Manager) Get(ctx context.Context, engineID pgtype.UUID) (Engine, error) {
	row, err := m.store.GetEngineWithConnection(ctx, engineID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, NewError(CodeNotFound, "", "", "engine not found")
	}
	if err != nil {
		return nil, err
	}
	if !row.Engine.Enabled {
		return nil, NewError(CodeUnavailable, row.Engine.Kind, "", "engine is disabled")
	}
	version := row.Engine.UpdatedAt.Time

	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.cache[engineID.Bytes]; ok {
		if cur.version.Equal(version) {
			return cur, nil
		}
		cur.retire()
		delete(m.cache, engineID.Bytes)
	}

	entry, ok := lookupKind(row.Engine.Kind)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrKindNotRegistered, row.Engine.Kind)
	}
	cfg, err := m.connectionConfig(row.CatalogConnection)
	if err != nil {
		return nil, err
	}
	er := EngineRow{
		ID:                  uuidString(row.Engine.ID),
		Name:                row.Engine.Name,
		Kind:                row.Engine.Kind,
		Class:               Class(row.Engine.Class),
		IdentityMode:        row.Engine.IdentityMode,
		CredentialsReadOnly: row.CatalogConnection.CredentialsReadOnly,
	}
	eng, err := entry.factory(ctx, er, cfg, m.dialer)
	if err != nil {
		return nil, err
	}
	me := &managed{Engine: eng, version: version, credentialsReadOnly: er.CredentialsReadOnly}
	m.cache[engineID.Bytes] = me
	return me, nil
}

// Close closes every cached engine once its sessions are closed.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.cache {
		e.retire()
		delete(m.cache, id)
	}
}

// connectionConfig decrypts the engine's connection. Kinds with a provider
// go through it (#11); others get the raw config and secrets.
func (m *Manager) connectionConfig(conn db.CatalogConnection) (connections.Config, error) {
	plain, err := catalogs.OpenCredentials(conn, m.keyring)
	if err != nil {
		return connections.Config{}, err
	}
	kind := connections.Kind(conn.Type)
	if m.providers != nil {
		cfg, err := providers.FromStored(m.providers, kind, conn.ID, conn.Url, conn.Config, plain)
		if err == nil || !errors.Is(err, connections.ErrUnknownKind) {
			return cfg, err
		}
	}
	cfg := connections.Config{ConnectionID: conn.ID, Kind: kind, Values: map[string]string{}, Secrets: map[string]string{}}
	if len(conn.Config) > 0 {
		if err := json.Unmarshal(conn.Config, &cfg.Values); err != nil {
			return connections.Config{}, fmt.Errorf("decode connection config: %w", err)
		}
	}
	if len(plain) > 0 {
		var secrets map[string]any
		if err := json.Unmarshal(plain, &secrets); err != nil {
			return connections.Config{}, fmt.Errorf("decode connection credentials: %w", err)
		}
		for k, v := range secrets {
			if s, ok := v.(string); ok {
				cfg.Secrets[k] = s
			}
		}
	}
	if cfg.Values["url"] == "" && conn.Url != "" {
		cfg.Values["url"] = conn.Url
	}
	return cfg, nil
}

func uuidString(u pgtype.UUID) string {
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
