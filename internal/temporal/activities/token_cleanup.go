package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/temporal/schedules"
)

// CleanupExpiredTokens prunes expired or revoked refresh tokens and expired or
// used password-reset tokens (#51). It runs on the daily token-cleanup
// schedule and never touches a live row.
func CleanupExpiredTokens(ctx context.Context) error {
	if err := defaultActivities.queries.DeleteExpiredRefreshTokens(ctx); err != nil {
		defaultActivities.passDone(schedules.TokenCleanup, err)
		return err
	}
	err := defaultActivities.queries.DeleteExpiredPasswordResetTokens(ctx)
	defaultActivities.passDone(schedules.TokenCleanup, err)
	return err
}
