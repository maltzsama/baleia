package obs

import (
	"log/slog"
	"testing"
)

func TestCapMemory(t *testing.T) {
	t.Parallel()
	// Without a cgroup limit this leaves GOMEMLIMIT unset; the test only
	// guards against a panic in the helper.
	CapMemory(slog.New(slog.DiscardHandler))
}
