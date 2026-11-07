package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00045, down00045)
}

const up00045Postgres = `
ALTER TABLE semantic_docs
    ADD COLUMN certified BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN certified_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN certified_at TIMESTAMPTZ;
`


const up00045MySQL = `
ALTER TABLE semantic_docs
    ADD COLUMN certified BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN certified_by CHAR(36) REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN certified_at DATETIME(6);
`


const down00045Postgres = `
ALTER TABLE semantic_docs
    DROP COLUMN IF EXISTS certified_at,
    DROP COLUMN IF EXISTS certified_by,
    DROP COLUMN IF EXISTS certified;
`


const down00045MySQL = `
ALTER TABLE semantic_docs
    DROP COLUMN certified_at,
    DROP COLUMN certified_by,
    DROP COLUMN certified;
`


func up00045(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00045Postgres, up00045MySQL)
}

func down00045(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00045Postgres, down00045MySQL)
}
