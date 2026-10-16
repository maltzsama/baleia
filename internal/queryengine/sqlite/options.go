package sqlite

import "sync"

// Options are the server-side SQLite settings, mirroring the provider's
// (docs/adr/0011): Root is BALEIA_SQLITE_ROOT, which confines every database
// file; AllowWrite is BALEIA_SQLITE_ALLOW_WRITE, which permits writes at all.
type Options struct {
	Root       string
	AllowWrite bool
}

var (
	optionsMu sync.RWMutex
	options   Options
)

// Configure sets the server-side options. It is called at the wiring sites
// (docs/adr/0007), next to providers.New, before any engine is built; both the
// manager and queryengine.Transient build SQLite engines through it.
func Configure(o Options) {
	optionsMu.Lock()
	options = o
	optionsMu.Unlock()
}

func currentOptions() Options {
	optionsMu.RLock()
	defer optionsMu.RUnlock()
	return options
}
