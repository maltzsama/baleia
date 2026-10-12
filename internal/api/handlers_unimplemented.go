package api

// This file contains stubs for every handler that is not implemented yet.
// Each method returns 501 Not Implemented.
// Implement them by moving them to domain-specific files:
//   handlers_users.go, handlers_teams.go, handlers_workspaces.go, etc.

import "context"


// ── Migrant ───────────────────────────────────────────────────────────────────

func (h *Handlers) ListMigrations(ctx context.Context, request ListMigrationsRequestObject) (ListMigrationsResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CreateMigration(ctx context.Context, request CreateMigrationRequestObject) (CreateMigrationResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetMigration(ctx context.Context, request GetMigrationRequestObject) (GetMigrationResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) StartMigration(ctx context.Context, request StartMigrationRequestObject) (StartMigrationResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CancelMigration(ctx context.Context, request CancelMigrationRequestObject) (CancelMigrationResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) ListMigrationItems(ctx context.Context, request ListMigrationItemsRequestObject) (ListMigrationItemsResponseObject, error) {
	return nil, errNotImplemented
}

// ── sentinel ──────────────────────────────────────────────────────────────────

type notImplementedError struct{}

func (e *notImplementedError) Error() string { return "not implemented" }

var errNotImplemented = &notImplementedError{}
