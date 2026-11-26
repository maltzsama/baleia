// Data policy administration (ADR 0028, #1146): a catalog's row filters and
// column masks, publishing them, the revisions and each engine's OPA state.
import { client } from '$lib/api/client';
import { listCatalogs, type CatalogSummary } from '$lib/catalogs/admin';
import { loadEngines } from '$lib/engines/client';
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

// An empty error body leaves result.error undefined on a failed response, so
// the status decides too.
function done(result: Result<unknown>): void {
	if (result.error !== undefined || !result.response?.ok) {
		throw new DataPolicyError(result.error, result.response?.status);
	}
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

/** Catalogs per page; the API's maximum. */
const catalogPage = 200;

/**
 * Every catalog whose data policies the caller manages (FD9): the server
 * filters on the same `manage_data_policies` action the screens check, page
 * after page, so none past the first page is missed.
 */
export async function listManageableCatalogs(): Promise<CatalogSummary[]> {
	const out: CatalogSummary[] = [];
	for (let offset = 0; ; offset += catalogPage) {
		const page = await listCatalogs({
			action: 'manage_data_policies',
			limit: catalogPage,
			offset
		});
		out.push(...page.filter((c) => c.id));
		if (page.length < catalogPage) return out;
	}
}

/**
 * The CodeMirror base of the engine that enforces a catalog's policies (one
 * with data policies on among the catalog's bindings), for showing policy
 * SQL; null when it cannot be told, which highlights as standard SQL.
 */
export async function policyDialect(catalog: CatalogSummary | null): Promise<string | null> {
	if (!catalog) return null;
	try {
		const engines = await loadEngines();
		const bound = new Set((catalog.bindings ?? []).map((b) => b.engine.id));
		const candidates = engines.filter((engine) => bound.has(engine.id));
		const engine = candidates.find((e) => e.data_policies) ?? candidates[0];
		return engine?.dialect?.codemirror_base ?? null;
	} catch {
		return null;
	}
}
