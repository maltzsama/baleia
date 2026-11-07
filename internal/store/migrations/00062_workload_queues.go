package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00062, down00062)
}

const up00062Postgres = `
-- Workload management (#399, ADR 0014): a run can wait in a per-engine queue
-- before it reaches the engine. Capacity is configured per (engine, class);
-- a missing row means the default that the API resolves.

ALTER TABLE query_executions DROP CONSTRAINT IF EXISTS query_executions_status_check;
ALTER TABLE query_executions
    ADD CONSTRAINT query_executions_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'cancelled', 'queued'));

ALTER TABLE query_executions
    ADD COLUMN queue_class   TEXT CHECK (queue_class IS NULL OR queue_class IN ('interactive', 'heavy', 'background')),
    ADD COLUMN queue_reason  TEXT,
    ADD COLUMN fairness_key  TEXT,
    ADD COLUMN queued_at     TIMESTAMPTZ,
    ADD COLUMN admitted_at   TIMESTAMPTZ,
    ADD COLUMN queue_wait_ms BIGINT;

-- Existing runs never waited in a queue.
UPDATE query_executions
   SET queued_at = started_at, admitted_at = started_at
 WHERE queued_at IS NULL;

CREATE TABLE engine_queue_capacity (
    engine_id        UUID        NOT NULL REFERENCES engines (id) ON DELETE CASCADE,
    class            TEXT        NOT NULL CHECK (class IN ('interactive', 'heavy', 'background')),
    max_running      INTEGER     NOT NULL CHECK (max_running >= 0),
    max_queued       INTEGER     NOT NULL CHECK (max_queued >= 0),
    queue_timeout_ms BIGINT      NOT NULL CHECK (queue_timeout_ms > 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (engine_id, class)
);

-- Nullable: no ceiling unless an admin sets one.
ALTER TABLE engines
    ADD COLUMN max_running_total INTEGER CHECK (max_running_total IS NULL OR max_running_total >= 0);
`

const up00062MySQL = `ALTER TABLE query_executions DROP CONSTRAINT IF EXISTS query_executions_status_check;
ALTER TABLE query_executions ADD CONSTRAINT query_executions_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'cancelled', 'queued'));
ALTER TABLE query_executions ADD COLUMN IF NOT EXISTS queue_class TEXT,
    ADD COLUMN IF NOT EXISTS queue_reason TEXT,
    ADD COLUMN IF NOT EXISTS fairness_key TEXT,
    ADD COLUMN IF NOT EXISTS queued_at DATETIME(6),
    ADD COLUMN IF NOT EXISTS admitted_at DATETIME(6),
    ADD COLUMN IF NOT EXISTS queue_wait_ms BIGINT,
    ADD CONSTRAINT query_executions_queue_class_check CHECK (queue_class IS NULL OR queue_class IN ('interactive', 'heavy', 'background'));
CREATE TABLE engine_queue_capacity (engine_id        CHAR(36)        NOT NULL,
    class            TEXT        NOT NULL,
    max_running      INTEGER     NOT NULL,
    max_queued       INTEGER     NOT NULL,
    queue_timeout_ms BIGINT      NOT NULL,
    created_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (engine_id, class(255)),
    CONSTRAINT engine_queue_capacity_engine_id_fkey FOREIGN KEY (engine_id) REFERENCES engines (id) ON DELETE CASCADE,
    CONSTRAINT engine_queue_capacity_class_check CHECK (class IN ('interactive', 'heavy', 'background')),
    CONSTRAINT engine_queue_capacity_max_running_check CHECK (max_running >= 0),
    CONSTRAINT engine_queue_capacity_max_queued_check CHECK (max_queued >= 0),
    CONSTRAINT engine_queue_capacity_queue_timeout_ms_check CHECK (queue_timeout_ms > 0)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
ALTER TABLE engines ADD COLUMN IF NOT EXISTS max_running_total INTEGER,
    ADD CONSTRAINT engines_max_running_total_check CHECK (max_running_total IS NULL OR max_running_total >= 0);`

const down00062Postgres = `
UPDATE query_executions SET status = 'cancelled' WHERE status = 'queued';

ALTER TABLE engines DROP COLUMN IF EXISTS max_running_total;
DROP TABLE IF EXISTS engine_queue_capacity;

ALTER TABLE query_executions
    DROP COLUMN IF EXISTS queue_wait_ms,
    DROP COLUMN IF EXISTS admitted_at,
    DROP COLUMN IF EXISTS queued_at,
    DROP COLUMN IF EXISTS fairness_key,
    DROP COLUMN IF EXISTS queue_reason,
    DROP COLUMN IF EXISTS queue_class;

ALTER TABLE query_executions DROP CONSTRAINT IF EXISTS query_executions_status_check;
ALTER TABLE query_executions
    ADD CONSTRAINT query_executions_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'cancelled'));
`

const down00062MySQL = `ALTER TABLE engines DROP COLUMN IF EXISTS max_running_total;
DROP TABLE IF EXISTS engine_queue_capacity;
ALTER TABLE query_executions DROP COLUMN IF EXISTS queue_wait_ms,
    DROP COLUMN IF EXISTS admitted_at,
    DROP COLUMN IF EXISTS queued_at,
    DROP COLUMN IF EXISTS fairness_key,
    DROP COLUMN IF EXISTS queue_reason,
    DROP COLUMN IF EXISTS queue_class;
ALTER TABLE query_executions DROP CONSTRAINT IF EXISTS query_executions_status_check;
ALTER TABLE query_executions ADD CONSTRAINT query_executions_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'cancelled'));`

func up00062(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00062Postgres, up00062MySQL)
}

func down00062(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00062Postgres, down00062MySQL)
}
