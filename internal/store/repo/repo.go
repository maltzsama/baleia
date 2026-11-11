package repo

import (
	"github.com/galileostudio/baleia/internal/db"
	"golang.org/x/sync/singleflight"
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
	*Webhooks
	// enqueue records a durable notification in the writer's transaction
	// (ADR 0022); nil when no shared layer is configured.
	enqueue func(tx *gorm.DB, channel, payload string) error
	// searchBuilds makes the searches of one scope share its index rebuild
	// (ADR 0023).
	searchBuilds singleflight.Group
	// vocabularies keep each scope's distinct terms in memory for the fuzzy
	// fallback of a search (ADR 0023).
	vocabularies vocabularies
}

// SetEnqueue wires the durable notification hook. Call it once, at startup.
func (r *Repo) SetEnqueue(fn func(tx *gorm.DB, channel, payload string) error) { r.enqueue = fn }

// enqueueTx records a notification in tx when a hook is set. A failed insert
// fails the surrounding transaction, so a write never commits without its
// notification (PostgreSQL aborts the transaction; returning the error keeps
// the same contract on MySQL/MariaDB).
func (r *Repo) enqueueTx(tx *gorm.DB, channel, payload string) error {
	if r.enqueue == nil {
		return nil
	}
	return r.enqueue(tx, channel, payload)
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
		Webhooks:  NewWebhooks(db),
	}
}
