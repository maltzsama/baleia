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
