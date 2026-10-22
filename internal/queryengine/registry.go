package queryengine

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/galileostudio/baleia/internal/connections"
)

// EngineRow is what a factory needs from an `engines` row (#13). It carries
// no secret: the decrypted connection arrives separately as a Config.
type EngineRow struct {
	ID           string
	Name         string
	Kind         string
	Class        Class
	IdentityMode string
	// CredentialsReadOnly is the admin assertion that the connection's user
	// cannot write (docs/adr/0011).
	CredentialsReadOnly bool
}

// Factory builds an engine instance. Every network dial goes through d
// (docs/adr/0011).
type Factory func(ctx context.Context, row EngineRow, cfg connections.Config, d *connections.Dialer) (Engine, error)

type kindEntry struct {
	class   Class
	factory Factory
}

var (
	kindsMu sync.RWMutex
	kinds   = map[string]kindEntry{}
)

// RegisterKind registers an adapter. Adapters call it from init(); a kind
// registered twice panics, as a programming error.
func RegisterKind(kind string, class Class, f Factory) {
	kindsMu.Lock()
	defer kindsMu.Unlock()
	if _, dup := kinds[kind]; dup {
		panic(fmt.Sprintf("queryengine: kind %q registered twice", kind))
	}
	kinds[kind] = kindEntry{class: class, factory: f}
}

func lookupKind(kind string) (kindEntry, bool) {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	e, ok := kinds[kind]
	return e, ok
}

// Kinds lists the registered kinds, sorted.
func Kinds() []string {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Transient builds an engine of kind from a connection's config without an
// `engines` row: never registered, never cached, for one check (a
// connection test, #12). The caller closes it.
func Transient(ctx context.Context, kind string, cfg connections.Config, d *connections.Dialer) (Engine, error) {
	e, ok := lookupKind(kind)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrKindNotRegistered, kind)
	}
	return e.factory(ctx, EngineRow{Name: "connection-test", Kind: kind, Class: e.class, IdentityMode: "connection"}, cfg, d)
}

// KindSpec describes one engine kind: the adapter packages export theirs and
// the composition root collects them into a Registry (#364).
type KindSpec struct {
	Name    string
	Class   Class
	Factory Factory
}

// Registry maps engine kinds to their factories. It is built once and only
// read afterwards, so it is safe for concurrent use without a lock.
type Registry struct {
	kinds map[string]kindEntry
}

// NewRegistry builds a registry from specs. A spec without a name or a
// factory, or a kind listed twice, is an error.
func NewRegistry(specs ...KindSpec) (*Registry, error) {
	r := &Registry{kinds: make(map[string]kindEntry, len(specs))}
	for _, s := range specs {
		if s.Name == "" || s.Factory == nil {
			return nil, fmt.Errorf("queryengine: incomplete kind spec %q", s.Name)
		}
		if _, dup := r.kinds[s.Name]; dup {
			return nil, fmt.Errorf("queryengine: kind %q registered twice", s.Name)
		}
		r.kinds[s.Name] = kindEntry{class: s.Class, factory: s.Factory}
	}
	return r, nil
}

// Has reports whether kind has an adapter.
func (r *Registry) Has(kind string) bool {
	_, ok := r.kinds[kind]
	return ok
}

// Kinds lists the registered kinds, sorted.
func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.kinds))
	for k := range r.kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Transient builds an engine of kind from a connection's config without an
// `engines` row: never cached, for one check (a connection test, #12). The
// caller closes it.
func (r *Registry) Transient(ctx context.Context, kind string, cfg connections.Config, d *connections.Dialer) (Engine, error) {
	e, ok := r.kinds[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrKindNotRegistered, kind)
	}
	return e.factory(ctx, EngineRow{Name: "connection-test", Kind: kind, Class: e.class, IdentityMode: "connection"}, cfg, d)
}

func (r *Registry) lookup(kind string) (kindEntry, bool) {
	e, ok := r.kinds[kind]
	return e, ok
}
