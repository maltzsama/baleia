package api

// Este arquivo contém stubs de todos os métodos ainda não implementados.
// Cada método retorna 501 Not Implemented.
// Implemente os métodos movendo-os para arquivos específicos por domínio:
//   handlers_users.go, handlers_teams.go, handlers_workspaces.go, etc.

import "context"

// ── Teams ─────────────────────────────────────────────────────────────────────

func (h *Handlers) ListTeams(ctx context.Context, request ListTeamsRequestObject) (ListTeamsResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CreateTeam(ctx context.Context, request CreateTeamRequestObject) (CreateTeamResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetTeam(ctx context.Context, request GetTeamRequestObject) (GetTeamResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) DeleteTeam(ctx context.Context, request DeleteTeamRequestObject) (DeleteTeamResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) ListTeamMembers(ctx context.Context, request ListTeamMembersRequestObject) (ListTeamMembersResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) AddTeamMember(ctx context.Context, request AddTeamMemberRequestObject) (AddTeamMemberResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) RemoveTeamMember(ctx context.Context, request RemoveTeamMemberRequestObject) (RemoveTeamMemberResponseObject, error) {
	return nil, errNotImplemented
}

// ── Workspaces ────────────────────────────────────────────────────────────────

func (h *Handlers) ListWorkspaces(ctx context.Context, request ListWorkspacesRequestObject) (ListWorkspacesResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CreateWorkspace(ctx context.Context, request CreateWorkspaceRequestObject) (CreateWorkspaceResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetWorkspace(ctx context.Context, request GetWorkspaceRequestObject) (GetWorkspaceResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) UpdateWorkspace(ctx context.Context, request UpdateWorkspaceRequestObject) (UpdateWorkspaceResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) ListWorkspaceCatalogAccess(ctx context.Context, request ListWorkspaceCatalogAccessRequestObject) (ListWorkspaceCatalogAccessResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GrantCatalogAccess(ctx context.Context, request GrantCatalogAccessRequestObject) (GrantCatalogAccessResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) RevokeCatalogAccess(ctx context.Context, request RevokeCatalogAccessRequestObject) (RevokeCatalogAccessResponseObject, error) {
	return nil, errNotImplemented
}

// ── Catalog Connections ───────────────────────────────────────────────────────

func (h *Handlers) ListCatalogConnections(ctx context.Context, request ListCatalogConnectionsRequestObject) (ListCatalogConnectionsResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CreateCatalogConnection(ctx context.Context, request CreateCatalogConnectionRequestObject) (CreateCatalogConnectionResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetCatalogConnection(ctx context.Context, request GetCatalogConnectionRequestObject) (GetCatalogConnectionResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) UpdateCatalogConnection(ctx context.Context, request UpdateCatalogConnectionRequestObject) (UpdateCatalogConnectionResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) DeleteCatalogConnection(ctx context.Context, request DeleteCatalogConnectionRequestObject) (DeleteCatalogConnectionResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) TestCatalogConnection(ctx context.Context, request TestCatalogConnectionRequestObject) (TestCatalogConnectionResponseObject, error) {
	return nil, errNotImplemented
}

// ── Catalog Explorer ──────────────────────────────────────────────────────────

func (h *Handlers) ListNamespaces(ctx context.Context, request ListNamespacesRequestObject) (ListNamespacesResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) ListTables(ctx context.Context, request ListTablesRequestObject) (ListTablesResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) ListColumns(ctx context.Context, request ListColumnsRequestObject) (ListColumnsResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) ListTableSnapshots(ctx context.Context, request ListTableSnapshotsRequestObject) (ListTableSnapshotsResponseObject, error) {
	return nil, errNotImplemented
}

// ── Query Editor ──────────────────────────────────────────────────────────────

func (h *Handlers) ListFolders(ctx context.Context, request ListFoldersRequestObject) (ListFoldersResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CreateFolder(ctx context.Context, request CreateFolderRequestObject) (CreateFolderResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) DeleteFolder(ctx context.Context, request DeleteFolderRequestObject) (DeleteFolderResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) ListSavedQueries(ctx context.Context, request ListSavedQueriesRequestObject) (ListSavedQueriesResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CreateSavedQuery(ctx context.Context, request CreateSavedQueryRequestObject) (CreateSavedQueryResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetSavedQuery(ctx context.Context, request GetSavedQueryRequestObject) (GetSavedQueryResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) UpdateSavedQuery(ctx context.Context, request UpdateSavedQueryRequestObject) (UpdateSavedQueryResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) DeleteSavedQuery(ctx context.Context, request DeleteSavedQueryRequestObject) (DeleteSavedQueryResponseObject, error) {
	return nil, errNotImplemented
}

// ── Executions ────────────────────────────────────────────────────────────────

func (h *Handlers) ListExecutions(ctx context.Context, request ListExecutionsRequestObject) (ListExecutionsResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) RunQuery(ctx context.Context, request RunQueryRequestObject) (RunQueryResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetExecution(ctx context.Context, request GetExecutionRequestObject) (GetExecutionResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CancelExecution(ctx context.Context, request CancelExecutionRequestObject) (CancelExecutionResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) ListStatements(ctx context.Context, request ListStatementsRequestObject) (ListStatementsResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetResults(ctx context.Context, request GetResultsRequestObject) (GetResultsResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) StreamExecution(ctx context.Context, request StreamExecutionRequestObject) (StreamExecutionResponseObject, error) {
	return nil, errNotImplemented
}

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

func (h *Handlers) ListRetrievers(ctx context.Context, request ListRetrieversRequestObject) (ListRetrieversResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) CreateRetriever(ctx context.Context, request CreateRetrieverRequestObject) (CreateRetrieverResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetRetriever(ctx context.Context, request GetRetrieverRequestObject) (GetRetrieverResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) UpdateRetriever(ctx context.Context, request UpdateRetrieverRequestObject) (UpdateRetrieverResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) DeleteRetriever(ctx context.Context, request DeleteRetrieverRequestObject) (DeleteRetrieverResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) TriggerRetrieverRefresh(ctx context.Context, request TriggerRetrieverRefreshRequestObject) (TriggerRetrieverRefreshResponseObject, error) {
	return nil, errNotImplemented
}

func (h *Handlers) GetRetrieverHistory(ctx context.Context, request GetRetrieverHistoryRequestObject) (GetRetrieverHistoryResponseObject, error) {
	return nil, errNotImplemented
}

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
