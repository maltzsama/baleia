-- +goose Up

-- Correlation search (#49): the audit viewer filters by request_id and trace_id.
CREATE INDEX idx_audit_events_request_id ON audit_events (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX idx_audit_events_trace_id ON audit_events (trace_id) WHERE trace_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_audit_events_trace_id;
DROP INDEX IF EXISTS idx_audit_events_request_id;
