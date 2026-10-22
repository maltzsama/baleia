package api

// This file contains stubs for every handler that is not implemented yet.
// Each method returns 501 Not Implemented.
// Implement them by moving them to domain-specific files:
//   handlers_users.go, handlers_teams.go, handlers_workspaces.go, etc.

import "context"

// ── Migrant ───────────────────────────────────────────────────────────────────

func (h *OpsHandlers) ListMigrations(ctx context.Context, request ListMigrationsRequestObject) (ListMigrationsResponseObject, error) {
	return nil, errNotImplemented
}

func (h *OpsHandlers) CreateMigration(ctx context.Context, request CreateMigrationRequestObject) (CreateMigrationResponseObject, error) {
	return nil, errNotImplemented
}

func (h *OpsHandlers) GetMigration(ctx context.Context, request GetMigrationRequestObject) (GetMigrationResponseObject, error) {
	return nil, errNotImplemented
}

func (h *OpsHandlers) StartMigration(ctx context.Context, request StartMigrationRequestObject) (StartMigrationResponseObject, error) {
	return nil, errNotImplemented
}

func (h *OpsHandlers) CancelMigration(ctx context.Context, request CancelMigrationRequestObject) (CancelMigrationResponseObject, error) {
	return nil, errNotImplemented
}

func (h *OpsHandlers) ListMigrationItems(ctx context.Context, request ListMigrationItemsRequestObject) (ListMigrationItemsResponseObject, error) {
	return nil, errNotImplemented
}

// ── sentinel ──────────────────────────────────────────────────────────────────

type notImplementedError struct{}

func (e *notImplementedError) Error() string { return "not implemented" }

var errNotImplemented = &notImplementedError{}
