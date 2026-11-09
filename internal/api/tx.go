package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// txRunner runs fn in one transaction: every dependent write of a handler
// goes through it (#376). The handlers get poolTx; tests give one that runs fn
// on their fake querier (#363).
type txRunner interface {
	InTx(ctx context.Context, fn func(q db.Querier) error) error
}

// poolTx runs fn in a GORM transaction over Baleia's pool. It remains the
// fixture seam for tests that only hold the pool.
type poolTx struct{ pool *pgxpool.Pool }

func (t poolTx) InTx(ctx context.Context, fn func(q db.Querier) error) error {
	sqlDB := stdlib.OpenDBFromPool(t.pool)
	defer func() { _ = sqlDB.Close() }()
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		return err
	}
	return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(repo.New(tx))
	})
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
