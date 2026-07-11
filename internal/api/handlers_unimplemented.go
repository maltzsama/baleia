package api

// Este arquivo contém stubs de todos os métodos ainda não implementados.
// Cada método retorna 501 Not Implemented.
// Implemente os métodos movendo-os para arquivos específicos por domínio:
//   handlers_users.go, handlers_teams.go, handlers_workspaces.go, etc.

import "context"


// ── Lenses ────────────────────────────────────────────────────────────────────

func (h *Handlers) ListLenses(ctx context.Context, request ListLensesRequestObject) (ListLensesResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CreateLens(ctx context.Context, request CreateLensRequestObject) (CreateLensResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetLens(ctx context.Context, request GetLensRequestObject) (GetLensResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) UpdateLens(ctx context.Context, request UpdateLensRequestObject) (UpdateLensResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) DeleteLens(ctx context.Context, request DeleteLensRequestObject) (DeleteLensResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) TriggerLens(ctx context.Context, request TriggerLensRequestObject) (TriggerLensResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetLensHistory(ctx context.Context, request GetLensHistoryRequestObject) (GetLensHistoryResponseObject, error) {
	return nil, errNotImplemented
}

// ── Retrievers ────────────────────────────────────────────────────────────────


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
