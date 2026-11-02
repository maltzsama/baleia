-- +goose Up
-- A-5.3 (#600): the per-engine switch that hands admission over to Trino's
-- resource groups. When on, Baleia's admission workflow stops granting slots
-- from its own per-class queue for this engine and lets Trino's resource group
-- hold the run; Baleia keeps only the parts Trino cannot express (fairness and
-- the per-agent limit). A run never waits in both queues.

ALTER TABLE engines
    ADD COLUMN trino_resource_groups_enabled BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE engines DROP COLUMN IF EXISTS trino_resource_groups_enabled;
