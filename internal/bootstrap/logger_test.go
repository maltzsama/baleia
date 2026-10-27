package bootstrap

import "log/slog"

// discard is the logger of tests that do not read the log.
var discard = slog.New(slog.DiscardHandler)
