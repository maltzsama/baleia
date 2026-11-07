package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00065, down00065)
}

const up00065Postgres = `
-- Ordered, admin-managed rules that set a run's class or max class (#399,
-- ADR 0014). 'match' is a closed schema: the CHECK rejects any key outside the
-- allow-list, and the class columns reject an unknown class.
CREATE TABLE workload_rules (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    position   INTEGER NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    match      JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (match - ARRAY[
            'workspace_id', 'team_id', 'role', 'user_id', 'agent_id', 'engine_id',
            'origin', 'saved_query_id', 'min_statements', 'max_statements'
        ] = '{}'::jsonb),
    class      TEXT NOT NULL CHECK (class IN ('interactive', 'heavy', 'background')),
    max_class  TEXT CHECK (max_class IS NULL OR max_class IN ('interactive', 'heavy', 'background')),
    weight     INTEGER NOT NULL DEFAULT 1 CHECK (weight > 0),
    created_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX workload_rules_position_idx ON workload_rules (position);
`

const up00065MySQL = `CREATE TABLE workload_rules (id         CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    position   INTEGER NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    {{bt}}match{{bt}}      JSON NOT NULL DEFAULT ('[]'),
    class      TEXT NOT NULL,
    max_class  TEXT,
    weight     INTEGER NOT NULL DEFAULT 1,
    created_by CHAR(36),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT workload_rules_class_check CHECK (class IN ('interactive', 'heavy', 'background')),
    CONSTRAINT workload_rules_max_class_check CHECK (max_class IS NULL OR max_class IN ('interactive', 'heavy', 'background')),
    CONSTRAINT workload_rules_weight_check CHECK (weight > 0),
    CONSTRAINT workload_rules_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS workload_rules_position_idx ON workload_rules (position);`

const down00065Postgres = `
DROP TABLE IF EXISTS workload_rules;
`

const down00065MySQL = `DROP TABLE IF EXISTS workload_rules;`

func up00065(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00065Postgres, up00065MySQL)
}

func down00065(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00065Postgres, down00065MySQL)
}
