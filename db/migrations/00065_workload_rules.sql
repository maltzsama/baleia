-- +goose Up
-- Ordered, admin-managed rules that set a run's class or max class (#399,
-- ADR 0014). `match` is a closed schema: the CHECK rejects any key outside the
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

-- +goose Down
DROP TABLE IF EXISTS workload_rules;
