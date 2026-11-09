package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"gorm.io/gorm"
)

// txRunner runs fn in one transaction: every dependent write of a handler
// goes through it (#376). The handlers get storeTx; tests that only hold the
// store build the same value over it (#363).
type txRunner interface {
	InTx(ctx context.Context, fn func(q db.Querier) error) error
}

// storeTx runs fn in a GORM transaction over the store (ADR 0020).
type storeTx struct{ db *gorm.DB }

func (t storeTx) InTx(ctx context.Context, fn func(q db.Querier) error) error {
	return t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(repo.New(tx))
	})
}

// inTx runs fn through the handlers' txRunner.
func (h *core) inTx(ctx context.Context, fn func(q db.Querier) error) error {
	return h.tx.InTx(ctx, fn)
}
