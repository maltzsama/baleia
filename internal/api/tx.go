package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// txRunner runs fn in one transaction: every dependent write of a handler
// goes through it (#376). The handlers get poolTx; tests give one that runs fn
// on their fake querier (#363).
type txRunner interface {
	InTx(ctx context.Context, fn func(q db.Querier) error) error
}

// poolTx runs fn in a transaction on Baleia's pool.
type poolTx struct{ pool *pgxpool.Pool }

func (t poolTx) InTx(ctx context.Context, fn func(q db.Querier) error) error {
	tx, err := t.pool.BeginTx(ctx, pgx.TxOptions{})
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

// inTx runs fn through the handlers' txRunner.
func (h *core) inTx(ctx context.Context, fn func(q db.Querier) error) error {
	return h.tx.InTx(ctx, fn)
}
