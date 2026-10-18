package auth

import (
	"errors"
	"fmt"
)

// User status values. Suspension is the supported way to disable an account;
// users are never deleted to revoke access (#256).
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// ErrAccountSuspended is returned by RequireActive for a non-active account.
var ErrAccountSuspended = errors.New("auth: account suspended")

// RequireActive is the single status gate for authentication paths. It accepts
// only the active status, so a new or unexpected value fails closed. Callers
// must use this helper instead of comparing status strings directly.
func RequireActive(status string) error {
	if status != StatusActive {
		return fmt.Errorf("%w (status %q)", ErrAccountSuspended, status)
	}
	return nil
}
