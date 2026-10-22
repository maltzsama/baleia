package api

import (
	"github.com/galileostudio/baleia/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

// dummyPasswordHash returns a bcrypt hash at the configured cost for the
// unknown-account and SSO-only paths. It is generated once per process and
// only ever compared against, never accepted (#51).
func (h *AuthHandlers) dummyPasswordHash() string {
	h.dummyHashOnce.Do(func() {
		cost := bcrypt.DefaultCost
		if h.settings.Auth.PasswordCost >= bcrypt.MinCost {
			cost = h.settings.Auth.PasswordCost
		}
		h.dummyHash, _ = auth.HashPasswordWithCost("baleia-dummy-password", cost)
	})
	return h.dummyHash
}
