package obs

import (
	"errors"
	"log/slog"

	"github.com/KimMachineGun/automemlimit/memlimit"
)

// CapMemory caps the Go heap to the container's cgroup limit so the GC runs
// before the OOM killer: Go otherwise targets the host's (effectively
// unlimited) memory and grows until the kernel SIGKILLs the process.
//
// Without a cgroup or a cgroup memory limit, GOMEMLIMIT stays unset (the
// pre-automemlimit behavior). Any other failure is a real misconfiguration
// worth surfacing.
func CapMemory(logger *slog.Logger) {
	if _, err := memlimit.Set(); err != nil {
		if errors.Is(err, memlimit.ErrNoCgroup) || errors.Is(err, memlimit.ErrNoLimit) {
			logger.Debug("no cgroup memory limit; GOMEMLIMIT left unset", "err", err)
		} else {
			logger.Warn("could not set GOMEMLIMIT from the cgroup", "err", err)
		}
	}
}
