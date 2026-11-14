// Catalog administration (#109): logical catalogs, bindings, metadata runs,
// grants, projections and impact. Reads never filter visibility client-side.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';
import { m } from '$lib/paraglide/messages';
import { catalogAccessStore } from '$lib/stores/catalogAccess.svelte';

export type CatalogSummary = components['schemas']['CatalogSummary'];
export type CatalogBindingRef = components['schemas']['CatalogBindingRef'];
export type Catalog = components['schemas']['Catalog'];
export type CatalogBinding = components['schemas']['CatalogBinding'];
export type CatalogImpact = components['schemas']['CatalogImpact'];
export type CatalogGrant = components['schemas']['CatalogGrant'];
export type BindingProjection = components['schemas']['BindingProjection'];
export type BindingSupport = components['schemas']['BindingSupport'];
export type MetadataSyncRun = components['schemas']['MetadataSyncRun'];
export type MetadataSyncStarted = components['schemas']['MetadataSyncStarted'];
export type UpdateBindingRequest = components['schemas']['UpdateBindingRequest'];
export type CreateBindingRequest = components['schemas']['CreateBindingRequest'];
export type UpdateCatalogRequest = components['schemas']['UpdateCatalogRequest'];

type Result<T> = { data?: T; error?: unknown; response?: Response };

export class CatalogAdminError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string
	) {
		super(message);
		this.name = 'CatalogAdminError';
	}
}

function fail<T>(result: Result<T>, fallback: string): T {
	if (result.error || result.data === undefined) {
		const body = result.error as { code?: string } | undefined;
		throw new CatalogAdminError(
			apiErrorMessage(result.error, fallback),
			result.response?.status,
			body?.code
		);
	}
	return result.data;
}

function failVoid(result: Result<unknown>, fallback: string): void {
	if (result.error) fail(result, fallback);
}

export async function listCatalogs(
	params: {
		q?: string;
		origin?: string;
		engine_id?: string;
		connection_id?: string;
		provider_kind?: string;
		attention?: boolean;
		action?: string;
		limit?: number;
		offset?: number;
	} = {}
): Promise<CatalogSummary[]> {
	return fail(
		await client.GET('/catalogs', { params: { query: params } }),
		'Could not load catalogs'
	);
}

export async function getCatalog(id: string): Promise<CatalogSummary> {
	return fail(
		await client.GET('/catalogs/{catalog_id}', { params: { path: { catalog_id: id } } }),
		'Could not load the catalog'
	);
}

export async function updateCatalogMeta(
	id: string,
	body: Pick<UpdateCatalogRequest, 'name'> & { description?: string | null }
): Promise<Catalog> {
	return fail(
		await client.PATCH('/catalogs/{catalog_id}', {
			params: { path: { catalog_id: id } },
			body
		}),
		'Could not update the catalog'
	);
}

export async function deleteCatalog(id: string): Promise<void> {
	const result = await client.DELETE('/catalogs/{catalog_id}', {
		params: { path: { catalog_id: id } }
	});
	failVoid(result, 'Could not delete the catalog');
	catalogAccessStore.changed();
}

export async function catalogImpact(id: string): Promise<CatalogImpact> {
	return fail(
		await client.GET('/catalogs/{catalog_id}/impact', { params: { path: { catalog_id: id } } }),
		'Could not load the impact'
	);
}

export async function catalogGrants(id: string): Promise<CatalogGrant[]> {
	return fail(
		await client.GET('/catalogs/{catalog_id}/grants', { params: { path: { catalog_id: id } } }),
		'Could not load the grants'
	);
}

export async function listSyncRuns(id: string): Promise<MetadataSyncRun[]> {
	return fail(
		await client.GET('/catalogs/{catalog_id}/metadata/sync-runs', {
			params: { path: { catalog_id: id } }
		}),
		'Could not load metadata runs'
	);
}

export async function syncMetadata(id: string): Promise<MetadataSyncStarted> {
	return fail(
		await client.POST('/catalogs/{catalog_id}/metadata/sync', {
			params: { path: { catalog_id: id } }
		}),
		'Could not start the metadata sync'
	);
}

export async function getBinding(id: string): Promise<CatalogBinding> {
	return fail(
		await client.GET('/bindings/{id}', { params: { path: { id } } }),
		'Could not load the binding'
	);
}

export async function bindingProjection(id: string): Promise<BindingProjection> {
	return fail(
		await client.GET('/bindings/{id}/projection', { params: { path: { id } } }),
		'Could not load the projection'
	);
}

export async function syncBinding(id: string): Promise<CatalogBinding> {
	return fail(
		await client.POST('/bindings/{id}/sync', { params: { path: { id } } }),
		'Could not sync the binding'
	);
}

export async function updateBinding(
	id: string,
	body: UpdateBindingRequest
): Promise<CatalogBinding> {
	return fail(
		await client.PATCH('/bindings/{id}', { params: { path: { id } }, body }),
		'Could not update the binding'
	);
}

export async function deleteBinding(id: string): Promise<void> {
	const result = await client.DELETE('/bindings/{id}', { params: { path: { id } } });
	failVoid(result, 'Could not delete the binding');
}

export async function createBinding(
	catalogId: string,
	body: CreateBindingRequest
): Promise<CatalogBinding> {
	return fail(
		await client.POST('/catalogs/{id}/bindings', {
			params: { path: { id: catalogId } },
			body
		}),
		'Could not create the binding'
	);
}

export async function bindingSupport(engineId: string): Promise<Record<string, BindingSupport>> {
	return fail(
		await client.GET('/engines/{id}/binding-support', { params: { path: { id: engineId } } }),
		'Could not load binding support'
	);
}

export async function grantCatalog(workspaceId: string, catalogId: string): Promise<void> {
	const result = await client.POST('/workspaces/{id}/catalog-access', {
		params: { path: { id: workspaceId } },
		body: { catalog_id: catalogId }
	});
	failVoid(result, 'Could not grant access');
	catalogAccessStore.changed();
}

export async function revokeCatalog(workspaceId: string, catalogId: string): Promise<void> {
	const result = await client.DELETE('/workspaces/{id}/catalog-access/{catalog_id}', {
		params: { path: { id: workspaceId, catalog_id: catalogId } }
	});
	failVoid(result, 'Could not revoke access');
	catalogAccessStore.changed();
}

/** The localized remediation sentence for a binding error code (FD7). */
export function bindingRemediation(code: string | null | undefined): string {
	switch (code) {
		case 'plugin_not_confirmed':
			return m.catalog_remediation_plugin_not_confirmed();
		case 'secret_mismatch':
			return m.catalog_remediation_secret_mismatch();
		case 'engine_load_failed':
			return m.catalog_remediation_engine_load_failed();
		case 'engine_unavailable':
			return m.catalog_remediation_engine_unavailable();
		case 'engine_catalog_exists':
			return m.catalog_remediation_engine_catalog_exists();
		default:
			return m.catalog_remediation_default();
	}
}
