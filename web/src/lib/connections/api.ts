// Typed calls for connection onboarding and management (#22).
//
// Every provider/engine kind comes from the API; this module never names one.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type ConnectionProvider = components['schemas']['ConnectionProvider'];
export type CatalogConnection = components['schemas']['CatalogConnection'];
export type Engine = components['schemas']['Engine'];
export type Catalog = components['schemas']['Catalog'];
export type CatalogBinding = components['schemas']['CatalogBinding'];
export type BindingSupport = components['schemas']['BindingSupport'];
export type TestResult = components['schemas']['TestCatalogConnectionResult'];
export type ConnectionCheck = components['schemas']['ConnectionCheck'];
export type Workspace = components['schemas']['Workspace'];
export type CatalogConnectionImpact = components['schemas']['CatalogConnectionImpact'];

/**
 * The create request, with `credentials` typed as the string map the API
 * actually accepts. The generated schema leaves the object open, which
 * openapi-typescript renders as `Record<string, never>`.
 */
export type ConnectionDraft = {
	name: string;
	type: string;
	config: Record<string, string>;
	credentials: Record<string, string>;
	default_readonly?: boolean;
	credentials_read_only?: boolean;
	intents?: string[];
};

export type CreateEngineInput = {
	name: string;
	connection_id: string;
};

export type CreateCatalogInput = {
	name: string;
	trino_catalog_name: string;
	warehouse: string;
	connector: string;
	description?: string;
};

export type GrantInput = {
	workspaceId: string;
	catalogId: string;
	permission: 'read' | 'write';
};

type ApiErrorBody = {
	code?: string;
	message?: string;
	errors?: { field?: string; message?: string }[];
	params?: Record<string, unknown>;
};

/** An API failure that keeps the bounded `code` and any `params` for the UI. */
export class ConnectionRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string,
		readonly params?: Record<string, unknown>,
		readonly fields?: Record<string, string>
	) {
		super(message);
		this.name = 'ConnectionRequestError';
	}
}

function toError(
	error: unknown,
	status: number | undefined,
	fallback: string
): ConnectionRequestError {
	if (error && typeof error === 'object') {
		const body = error as ApiErrorBody;
		const message = body.errors?.[0]?.message ?? body.message ?? fallback;
		const fields: Record<string, string> = {};
		for (const item of body.errors ?? []) {
			if (item.field && item.message) fields[item.field] = item.message;
		}
		return new ConnectionRequestError(message, status, body.code, body.params, fields);
	}
	if (error instanceof Error)
		return new ConnectionRequestError(error.message, status, undefined, undefined);
	return new ConnectionRequestError(apiErrorMessage(error, fallback), status, undefined, undefined);
}

/** Every provider with its roles and config schema. */
export async function loadProviders(): Promise<ConnectionProvider[]> {
	const { data, error, response } = await client.GET('/connection-providers');
	if (error || !data) throw toError(error, response?.status, 'Could not load connection providers');
	return data;
}

/** Connections the caller may administer. */
export async function listConnections(): Promise<CatalogConnection[]> {
	const { data, error, response } = await client.GET('/catalog-connections');
	if (error || !data) throw toError(error, response?.status, 'Could not load connections');
	return data;
}

export async function createConnection(draft: ConnectionDraft): Promise<CatalogConnection> {
	const { data, error, response } = await client.POST('/catalog-connections', {
		body: draft as unknown as components['schemas']['CreateCatalogConnectionRequest']
	});
	if (error || !data) throw toError(error, response?.status, 'Could not create the connection');
	return data;
}

export async function updateConnection(
	id: string,
	draft: Omit<ConnectionDraft, 'type' | 'intents'>
): Promise<CatalogConnection> {
	const { data, error, response } = await client.PATCH('/catalog-connections/{id}', {
		params: { path: { id } },
		body: draft as unknown as components['schemas']['UpdateCatalogConnectionRequest']
	});
	if (error || !data) throw toError(error, response?.status, 'Could not update the connection');
	return data;
}

export async function deleteConnection(id: string): Promise<void> {
	const { error, response } = await client.DELETE('/catalog-connections/{id}', {
		params: { path: { id } }
	});
	if (error) throw toError(error, response?.status, 'Could not delete the connection');
}

/** Tests settings that have not been saved. Persists nothing. */
export async function testDraft(draft: ConnectionDraft): Promise<TestResult> {
	const { data, error, response } = await client.POST('/catalog-connections/test', {
		body: draft as unknown as components['schemas']['TestConnectionRequest']
	});
	if (error || !data) throw toError(error, response?.status, 'The connection test failed');
	return data;
}

/** One connection with its non-secret config and allowed actions. */
export async function getConnection(id: string): Promise<CatalogConnection> {
	const { data, error, response } = await client.GET('/catalog-connections/{id}', {
		params: { path: { id } }
	});
	if (error || !data) throw toError(error, response?.status, 'Could not load the connection');
	return data;
}

/** What depends on a connection before deleting it (#109). */
export async function getConnectionImpact(id: string) {
	const { data, error, response } = await client.GET('/catalog-connections/{id}/impact', {
		params: { path: { id } }
	});
	if (error || !data)
		throw toError(error, response?.status, 'Could not load the connection impact');
	return data;
}

/** Re-tests a stored connection with its stored credentials. */
export async function testStored(id: string): Promise<TestResult> {
	const { data, error, response } = await client.POST('/catalog-connections/{id}/test', {
		params: { path: { id } }
	});
	if (error || !data) throw toError(error, response?.status, 'The connection test failed');
	return data;
}

export async function listEngines(): Promise<Engine[]> {
	const { data, error, response } = await client.GET('/engines');
	if (error || !data) throw toError(error, response?.status, 'Could not load engines');
	return data;
}

/** The source kinds an engine can bind, with connectors and reserved names. */
export async function bindingSupport(engineId: string): Promise<Record<string, BindingSupport>> {
	const { data, error, response } = await client.GET('/engines/{id}/binding-support', {
		params: { path: { id: engineId } }
	});
	if (error || !data)
		throw toError(error, response?.status, 'Could not load the engine binding support');
	return data;
}

export async function createEngine(input: CreateEngineInput): Promise<Engine> {
	const { data, error, response } = await client.POST('/engines', { body: input });
	if (error || !data) throw toError(error, response?.status, 'Could not register the engine');
	return data;
}

export async function listConnectionCatalogs(connectionId: string): Promise<Catalog[]> {
	const { data, error, response } = await client.GET('/connections/{connection_id}/catalogs', {
		params: { path: { connection_id: connectionId } }
	});
	if (error || !data) throw toError(error, response?.status, 'Could not load catalogs');
	return data;
}

/** Creates a logical catalog and its managed binding on the default engine. */
export async function createCatalog(
	connectionId: string,
	input: CreateCatalogInput
): Promise<Catalog> {
	const { data, error, response } = await client.POST('/connections/{connection_id}/catalogs', {
		params: { path: { connection_id: connectionId } },
		body: {
			name: input.name,
			trino_catalog_name: input.trino_catalog_name,
			warehouse: input.warehouse,
			connector: input.connector,
			description: input.description,
			enabled: true
		}
	});
	if (error || !data) throw toError(error, response?.status, 'Could not create the catalog');
	return data;
}

export async function listCatalogBindings(catalogId: string): Promise<CatalogBinding[]> {
	const { data, error, response } = await client.GET('/catalogs/{id}/bindings', {
		params: { path: { id: catalogId } }
	});
	if (error || !data) throw toError(error, response?.status, 'Could not load bindings');
	return data;
}

export async function grantCatalogAccess(input: GrantInput): Promise<void> {
	const { error, response } = await client.POST('/workspaces/{id}/catalog-access', {
		params: { path: { id: input.workspaceId } },
		body: { catalog_id: input.catalogId, permission: input.permission }
	});
	if (error) throw toError(error, response?.status, 'Could not grant access');
}

/** Workspaces the caller can grant catalogs to (they carry manage_grants). */
export async function listGrantableWorkspaces(): Promise<Workspace[]> {
	const { data, error, response } = await client.GET('/workspaces');
	if (error || !data) throw toError(error, response?.status, 'Could not load workspaces');
	return data.filter((w) => w.allowed_actions.includes('manage_grants'));
}
