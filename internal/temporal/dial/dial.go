// Package dial connects to Temporal at boot, retrying for a while instead of
// failing on the first refused connection (#87): a process that cannot reach
// Temporal exits with an error log, which a crash loop makes visible.
package dial

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	tlog "go.temporal.io/sdk/log"
)

// Temporal dials host until it answers or wait elapses. The SDK logs through
// logger, like the rest of the process (#379). handler reports the SDK's
// metrics; nil for none. interceptors are the client interceptors (the
// tracing interceptor, #47); nil for none.
func Temporal(
	ctx context.Context,
	logger *slog.Logger,
	host string,
	handler client.MetricsHandler,
	wait time.Duration,
	interceptors []interceptor.ClientInterceptor,
) (client.Client, error) {
	deadline := time.Now().Add(wait)
	backoff := 500 * time.Millisecond
	for {
		c, err := client.DialContext(ctx, client.Options{
			HostPort:       host,
			Logger:         tlog.NewStructuredLogger(logger),
			MetricsHandler: handler,
			Interceptors:   interceptors,
		})
		if err == nil {
			return c, nil
		}
		if time.Now().Add(backoff).After(deadline) {
			return nil, fmt.Errorf("temporal at %s unreachable for %s: %w", host, wait, err)
		}
		logger.WarnContext(ctx, "temporal not reachable yet; retrying", "host", host, "in", backoff, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}
