package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
)

// inTx runs fn in one transaction: every dependent write of a handler goes
// through it (#376). Without a pool (unit tests) it runs fn on the handler's
// querier directly.
func (h *Handlers) inTx(ctx context.Context, fn func(q db.Querier) error) error {
	if h.pool == nil {
		return fn(h.queries)
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	// Rollback after Commit is a no-op; on the error paths it is the cleanup.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
