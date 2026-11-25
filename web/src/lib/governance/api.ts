// Data policy administration (ADR 0028, #1146): a catalog's row filters and
// column masks, publishing them, the revisions and each engine's OPA state.
import { client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type DataPolicy = components['schemas']['DataPolicy'];
export type DataPolicySubject = components['schemas']['DataPolicySubject'];
export type DataPolicyKind = components['schemas']['DataPolicyKind'];
export type DataPolicyMode = components['schemas']['DataPolicyMode'];
export type DataPolicyTemplate = components['schemas']['DataPolicyTemplate'];
export type DataPolicyWrites = components['schemas']['DataPolicyWrites'];
export type DataPolicyWriteEffect = components['schemas']['DataPolicyWriteEffect'];
export type CatalogDataPolicies = components['schemas']['CatalogDataPolicies'];
export type CreateDataPolicyRequest = components['schemas']['CreateDataPolicyRequest'];
export type DataPolicyDefinerView = components['schemas']['DataPolicyDefinerView'];
export type DataPolicyRevision = components['schemas']['DataPolicyRevision'];
export type DataPolicyProblem = components['schemas']['DataPolicyProblem'];
export type DataPolicyStatus = components['schemas']['DataPolicyStatus'];
export type DataPolicyEngineState = components['schemas']['DataPolicyEngineState'];

type Result<T> = { data?: T; error?: unknown; response?: Response };

/**
 * A failed call. `body` is the API error body, so the page renders it through
 * the error catalog (FD7); `problems` lists why a publish was refused.
 */
export class DataPolicyError extends Error {
	constructor(
		readonly body: unknown,
		readonly status?: number
	) {
		super('data policy request failed');
		this.name = 'DataPolicyError';
	}

	get code(): string | undefined {
		return (this.body as { code?: string } | undefined)?.code;
	}

	get problems(): DataPolicyProblem[] {
		return (this.body as { problems?: DataPolicyProblem[] } | undefined)?.problems ?? [];
	}
}

function ok<T>(result: Result<T>): T {
	if (result.error !== undefined || result.data === undefined) {
		throw new DataPolicyError(result.error, result.response?.status);
	}
	return result.data;
}

function done(result: Result<unknown>): void {
	if (result.error !== undefined) throw new DataPolicyError(result.error, result.response?.status);
}

export async function listCatalogPolicies(catalogId: string): Promise<CatalogDataPolicies> {
	return ok(
		await client.GET('/catalogs/{id}/data-policies', { params: { path: { id: catalogId } } })
	);
}

export async function createPolicy(
	catalogId: string,
	body: CreateDataPolicyRequest
): Promise<DataPolicy> {
	return ok(
		await client.POST('/catalogs/{id}/data-policies', {
			params: { path: { id: catalogId } },
			body
		})
	);
}

export async function updatePolicy(
	id: string,
	body: CreateDataPolicyRequest & { version: number }
): Promise<DataPolicy> {
	return ok(await client.PUT('/data-policies/{id}', { params: { path: { id } }, body }));
}

export async function deletePolicy(id: string): Promise<void> {
	done(await client.DELETE('/data-policies/{id}', { params: { path: { id } } }));
}

export async function publishCatalog(catalogId: string): Promise<DataPolicyRevision> {
	return ok(
		await client.POST('/catalogs/{id}/data-policies/publish', {
			params: { path: { id: catalogId } }
		})
	);
}

export async function listDefinerViews(catalogId: string): Promise<DataPolicyDefinerView[]> {
	return ok(
		await client.GET('/catalogs/{id}/data-policies/definer-views', {
			params: { path: { id: catalogId } }
		})
	);
}

export async function listRevisions(limit = 50): Promise<DataPolicyRevision[]> {
	return ok(await client.GET('/data-policies/revisions', { params: { query: { limit } } }));
}

export async function rollbackRevision(id: string): Promise<DataPolicyRevision> {
	return ok(
		await client.POST('/data-policies/revisions/{id}/rollback', { params: { path: { id } } })
	);
}

export async function getStatus(): Promise<DataPolicyStatus> {
	return ok(await client.GET('/data-policies/status'));
}
