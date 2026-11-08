package repo

import "gorm.io/gorm"

// Repo is the GORM-backed store for the domains migrated off sqlc. Embedding
// the per-domain stores lets one value satisfy the narrow interfaces of the
// services that need several of them.
type Repo struct {
	*Users
	*Providers
	*Sessions
	*APIKeys
}

// New builds every domain store over one connection.
func New(db *gorm.DB) *Repo {
	return &Repo{
		Users:     NewUsers(db),
		Providers: NewProviders(db),
		Sessions:  NewSessions(db),
		APIKeys:   NewAPIKeys(db),
	}
}
