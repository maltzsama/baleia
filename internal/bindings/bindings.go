// Package bindings is the binder contract (#80, docs/adr/0002): a binding
// makes a source catalog visible inside a federated engine. A binder owns
// everything engine-specific about that — the projection of a connection
// into the engine's catalog properties and the engine-side persistence (for
// Trino, the catalog store plugin's registry and CREATE CATALOG). Providers
// know nothing about engines; direct engines have no binder (their catalogs
// are native bindings).
package bindings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	// ErrInvalid is a binding the binder cannot project (bad name, malformed
	// extra properties, missing source fields).
	ErrInvalid = errors.New("invalid binding")
	// ErrPropertyNotOverridable is an extra property that would replace a
	// protected one (endpoints, credentials, security mode).
	ErrPropertyNotOverridable = errors.New("property_not_overridable")
	// ErrReservedName is an engine catalog name the engine or binder reserves.
	ErrReservedName = errors.New("reserved catalog name")
	// ErrUnsupportedSource is a source kind or connector the binder cannot bind.
	ErrUnsupportedSource = errors.New("source not bindable on this engine")
)

// PropertyError is an ErrPropertyNotOverridable or ErrInvalid for one key.
type PropertyError struct {
	Key string
	Err error
}

func (e *PropertyError) Error() string { return fmt.Sprintf("%s: %v", e.Key, e.Err) }
func (e *PropertyError) Unwrap() error { return e.Err }

// Projection is what a binder needs to materialize a binding in its engine.
type Projection struct {
	EngineCatalog string            // the catalog's name inside the engine
	Connector     string            // engine-side connector ("iceberg", "postgresql", …)
	Properties    map[string]string // REAL values
	SecretKeys    []string          // keys whose values never appear in DDL text or logs
	Version       string            // stable hash of the projection
}

// Observed is one catalog the engine reports.
type Observed struct {
	EngineCatalog string
	Present       bool
	Connector     string
}

// Confirmation says whether the engine applied a projection.
type Confirmation struct {
	Confirmed bool
	Reason    string // "plugin_not_confirmed", "version_mismatch", …
}

// BindingSpec is a binding as the binder sees it.
type BindingSpec struct {
	EngineCatalog string
	Connector     string // among Supports()[kind]; "" = the first
	SourceRef     string // warehouse, database, …
	// ExtraProps merge over the defaults; protected keys are refused.
	ExtraProps map[string]string
}

// EngineRef names the engine a binder acts on. ClusterID is the Trino
// binder's registry key (trino_clusters, the plugin contract).
type EngineRef struct {
	ID        pgtype.UUID
	ClusterID pgtype.UUID
}

// Binder materializes bindings in one engine kind.
type Binder interface {
	EngineKind() string
	// Supports lists the source kinds the binder binds and their connectors,
	// the default first.
	Supports() map[connections.Kind][]string
	// ProtectedKeyPatterns are the extra-property keys that cannot be
	// overridden (glob patterns, for the API and UI).
	ProtectedKeyPatterns() []string
	// ReservedNames are engine catalog names that cannot be used.
	ReservedNames() []string
	// Project builds the projection of one binding; pure, no I/O.
	Project(b BindingSpec, src connections.Config) (Projection, error)

	Create(ctx context.Context, eng EngineRef, p Projection) error
	// Replace applies a changed projection under the same name.
	Replace(ctx context.Context, eng EngineRef, p Projection) error
	// Rename moves a binding to a new name (create then drop).
	Rename(ctx context.Context, eng EngineRef, oldName string, p Projection) error
	Drop(ctx context.Context, eng EngineRef, name string) error
	Confirm(ctx context.Context, eng EngineRef, p Projection) (Confirmation, error)
	Observe(ctx context.Context, eng EngineRef) ([]Observed, error)
}

var (
	mu      sync.RWMutex
	binders = map[string]Binder{}
)

// Register makes a binder available for its engine kind; wiring sites call
// it with binders they construct.
func Register(b Binder) {
	mu.Lock()
	defer mu.Unlock()
	binders[b.EngineKind()] = b
}

// For returns the binder of an engine kind.
func For(engineKind string) (Binder, bool) {
	mu.RLock()
	defer mu.RUnlock()
	b, ok := binders[engineKind]
	return b, ok
}

// Connector picks the binding's connector for a source kind: the requested
// one if the binder supports it, else the default.
func Connector(b Binder, kind connections.Kind, requested string) (string, error) {
	supported, ok := b.Supports()[kind]
	if !ok || len(supported) == 0 {
		return "", fmt.Errorf("%w: %s on %s", ErrUnsupportedSource, kind, b.EngineKind())
	}
	if requested == "" {
		return supported[0], nil
	}
	if !slices.Contains(supported, requested) {
		return "", fmt.Errorf("%w: connector %q for %s (supported: %v)", ErrUnsupportedSource, requested, kind, supported)
	}
	return requested, nil
}
