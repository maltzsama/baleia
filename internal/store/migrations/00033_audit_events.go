package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00033, down00033)
}

const up00033Postgres = `
-- Append-only audit trail (#45). The application connects as the role that
-- owns the table, and an owner keeps every privilege whatever REVOKE says:
-- append-only is enforced by a trigger. Partitioned by month; there is no
-- DEFAULT partition (rows in it would make creating the matching range
-- partition fail later), so a missing partition makes Record fail loudly.
CREATE TABLE audit_events (
    id            BIGINT GENERATED ALWAYS AS IDENTITY,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_kind    TEXT        NOT NULL CHECK (actor_kind IN ('user', 'service', 'agent', 'anonymous')),
    actor_id      TEXT,
    auth_method   TEXT,
    -- Bounded vocabulary, internal/audit/actions.go: <resource>.<verb>.
    action        TEXT        NOT NULL CHECK (action ~ '^[a-z_]+\.[a-z_]+$'),
    resource_type TEXT        NOT NULL,
    resource_id   TEXT,
    workspace_id  UUID,
    outcome       TEXT        NOT NULL CHECK (outcome IN ('allowed', 'denied', 'error')),
    reason        TEXT,
    request_id    TEXT,
    trace_id      TEXT,
    client_ip     INET,
    user_agent    TEXT,
    -- {"field": {"from": …, "to": …}}; secret values are stored as "***".
    changes       JSONB,
    PRIMARY KEY (occurred_at, id)
) PARTITION BY RANGE (occurred_at);

CREATE INDEX audit_events_resource ON audit_events (resource_type, resource_id, occurred_at DESC);
CREATE INDEX audit_events_actor    ON audit_events (actor_id, occurred_at DESC);
CREATE INDEX audit_events_ws       ON audit_events (workspace_id, occurred_at DESC);

-- audit_ensure_partitions creates the monthly partitions of the current
-- month and the next months_ahead ones, if missing. It never drops one: the
-- worker calls it daily (schedule audit-partitions); retention is the
-- explicit operator command 'migrate -cmd=audit-prune'.
CREATE FUNCTION audit_ensure_partitions(months_ahead int) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    m date := date_trunc('month', now())::date;
BEGIN
    FOR i IN 0..months_ahead LOOP
        EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_events FOR VALUES FROM (%L) TO (%L)',
                       'audit_events_' || to_char(m + make_interval(months => i), 'YYYYMM'),
                       m + make_interval(months => i), m + make_interval(months => i + 1));
    END LOOP;
END $$;

SELECT audit_ensure_partitions(2);
CREATE FUNCTION audit_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only (% blocked)', TG_OP;
END $$;

CREATE TRIGGER audit_events_no_update BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();
`

const up00033MySQL = `CREATE TABLE audit_events (id            BIGINT AUTO_INCREMENT,
    occurred_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    actor_kind    TEXT        NOT NULL,
    actor_id      TEXT,
    auth_method   TEXT,
    action        TEXT        NOT NULL,
    resource_type TEXT        NOT NULL,
    resource_id   TEXT,
    workspace_id  CHAR(36),
    outcome       TEXT        NOT NULL,
    reason        TEXT,
    request_id    TEXT,
    trace_id      TEXT,
    client_ip     VARCHAR(64),
    user_agent    TEXT,
    changes       JSON,
    PRIMARY KEY (id),
    CONSTRAINT audit_events_actor_kind_check CHECK (actor_kind IN ('user', 'service', 'agent', 'anonymous')),
    CONSTRAINT audit_events_action_check CHECK (action REGEXP '^[a-z_]+\.[a-z_]+$'),
    CONSTRAINT audit_events_outcome_check CHECK (outcome IN ('allowed', 'denied', 'error'))) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS audit_events_resource ON audit_events (resource_type(255), resource_id(255), occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_actor    ON audit_events (actor_id(255), occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_ws       ON audit_events (workspace_id, occurred_at DESC);`

const down00033Postgres = `
DROP TABLE IF EXISTS audit_events;
DROP FUNCTION IF EXISTS audit_ensure_partitions(int);
DROP FUNCTION IF EXISTS audit_events_append_only();
`

const down00033MySQL = `DROP TABLE IF EXISTS audit_events;`

func up00033(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00033Postgres, up00033MySQL)
}

func down00033(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00033Postgres, down00033MySQL)
}
