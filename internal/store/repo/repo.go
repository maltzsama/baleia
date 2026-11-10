package repo

import (
	"github.com/galileostudio/baleia/internal/db"
	"gorm.io/gorm"
)

// Repo is the GORM-backed store for the domains migrated off sqlc. Embedding
// the per-domain stores lets one value satisfy the narrow interfaces of the
// services that need several of them.
var _ db.Querier = (*Repo)(nil)

type Repo struct {
	*Users
	*Providers
	*Sessions
	*APIKeys
	*Teams
	*Agents
	// enqueue records a durable notification in the writer's transaction
	// (ADR 0022); nil when no shared layer is configured.
	enqueue func(tx *gorm.DB, channel, payload string) error
}

// SetEnqueue wires the durable notification hook. Call it once, at startup.
func (r *Repo) SetEnqueue(fn func(tx *gorm.DB, channel, payload string) error) { r.enqueue = fn }

// enqueueTx records a notification in tx when a hook is set. It is
// best-effort: a failed insert must not fail the domain write it accompanies.
func (r *Repo) enqueueTx(tx *gorm.DB, channel, payload string) {
	if r.enqueue != nil {
		_ = r.enqueue(tx, channel, payload)
	}
}

// New builds every domain store over one connection.
func New(db *gorm.DB) *Repo {
	return &Repo{
		Users:     NewUsers(db),
		Providers: NewProviders(db),
		Sessions:  NewSessions(db),
		APIKeys:   NewAPIKeys(db),
		Teams:     NewTeams(db),
		Agents:    NewAgents(db),
	}
}
