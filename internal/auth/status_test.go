package auth

import (
	"errors"
	"testing"
)

func TestRequireActive(t *testing.T) {
	if err := RequireActive(StatusActive); err != nil {
		t.Fatalf("active status rejected: %v", err)
	}

	for _, status := range []string{StatusSuspended, "", "deleted", "ACTIVE"} {
		err := RequireActive(status)
		if !errors.Is(err, ErrAccountSuspended) {
			t.Errorf("RequireActive(%q) = %v, want ErrAccountSuspended", status, err)
		}
	}
}
