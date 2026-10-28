// Engine administration calls (#110) — list, register, test, discover, impact,
// reconcile and delete. Reads that the workbench caches live in ./client.ts.
import { m } from '$lib/paraglide/messages';
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type Engine = components['schemas']['Engine'];
export type EngineImpact = components['schemas']['EngineImpact'];
export type EngineObservation = components['schemas']['EngineObservation'];
export type DiscoverResult = components['schemas']['DiscoverResult'];
export type CatalogBinding = components['schemas']['CatalogBinding'];
export type CatalogConnection = components['schemas']['CatalogConnection'];
export type ReconcileRun = components['schemas']['ReconcileRun'];
export type ReconcileAction = components['schemas']['ReconcileAction'];
export type TestResult = components['schemas']['TestCatalogConnectionResult'];
export type CreateEngineRequest = components['schemas']['CreateEngineRequest'];

type Result<T> = { data?: T; error?: unknown; response?: Response };

export class EngineAdminError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string
	) {
		super(message);
		this.name = 'EngineAdminError';
	}
}

function fail<T>(result: Result<T>, fallback: string): T {
	if (result.error || result.data === undefined) {
		const body = result.error as { code?: string } | undefined;
		throw new EngineAdminError(
			apiErrorMessage(result.error, fallback),
			result.response?.status,
			body?.code
		);
	}
	return result.data;
}

export async function listEngines(): Promise<Engine[]> {
	return fail(await client.GET('/engines'), 'Could not load engines');
}

export async function getEngine(id: string): Promise<Engine> {
	return fail(
		await client.GET('/engines/{id}', { params: { path: { id } } }),
		'Could not load the engine'
	);
}

export async function createEngine(body: CreateEngineRequest): Promise<Engine> {
	return fail(await client.POST('/engines', { body }), 'Could not register the engine');
}

export async function updateEngine(id: string, body: { enabled?: boolean }): Promise<Engine> {
	return fail(
		await client.PATCH('/engines/{id}', { params: { path: { id } }, body }),
		'Could not update the engine'
	);
}

export async function deleteEngine(id: string): Promise<void> {
	const result = await client.DELETE('/engines/{id}', { params: { path: { id } } });
	if (result.error) fail(result, 'Could not delete the engine');
}

export async function testEngine(id: string): Promise<TestResult> {
	return fail(
		await client.POST('/engines/{id}/test', { params: { path: { id } } }),
		'Could not test the engine'
	);
}

export async function engineImpact(id: string): Promise<EngineImpact> {
	return fail(
		await client.GET('/engines/{id}/impact', { params: { path: { id } } }),
		'Could not load the impact'
	);
}

export async function discoverEngineCatalogs(id: string): Promise<DiscoverResult> {
	return fail(
		await client.POST('/engines/{id}/discover', { params: { path: { id } } }),
		'Could not discover catalogs'
	);
}

export async function engineObservations(id: string): Promise<EngineObservation[]> {
	return fail(
		await client.GET('/engines/{id}/observations', { params: { path: { id } } }),
		'Could not load observed catalogs'
	);
}

export async function engineBindings(id: string): Promise<CatalogBinding[]> {
	return fail(
		await client.GET('/engines/{id}/bindings', { params: { path: { id } } }),
		'Could not load the engine catalogs'
	);
}

export async function reconcileEngine(id: string, dryRun: boolean): Promise<ReconcileRun> {
	return fail(
		await client.POST('/engines/{id}/reconcile', {
			params: { path: { id }, query: { dry_run: dryRun } }
		}),
		'Could not reconcile the engine'
	);
}

export async function listReconcileRuns(id: string): Promise<ReconcileRun[]> {
	return fail(
		await client.GET('/engines/{id}/reconcile-runs', { params: { path: { id } } }),
		'Could not load reconcile runs'
	);
}

export async function getReconcileRun(id: string, runId: string): Promise<ReconcileRun> {
	return fail(
		await client.GET('/engines/{id}/reconcile-runs/{run_id}', {
			params: { path: { id, run_id: runId } }
		}),
		'Could not load the reconcile run'
	);
}

export async function listCatalogConnections(params: {
	action?: string;
	without_engine?: boolean;
}): Promise<CatalogConnection[]> {
	return fail(
		await client.GET('/catalog-connections', { params: { query: params } }),
		'Could not load catalog connections'
	);
}

/** The read-only guarantee shown on the list and the overview (#110). */
export function readOnlyGuarantee(engine: Engine): {
	label: string;
	tone: 'success' | 'warning' | 'danger';
	detail: string;
} {
	if (engine.capabilities?.read_only === 'enforced') {
		return {
			label: m.engines_admin_read_only_enforced(),
			tone: 'success',
			detail: 'The engine itself rejects writes, so read-only grants and agents are safe.'
		};
	}
	if (engine.credentials_read_only) {
		return {
			label: m.engines_admin_read_only_via_credentials(),
			tone: 'warning',
			detail:
				'The engine can write; the connection asserts its credentials only allow reads. Read-only grants rely on that assertion.'
		};
	}
	return {
		label: m.engines_admin_read_only_not_available(),
		tone: 'danger',
		detail:
			'Read-only grants and agents cannot use this engine until an administrator asserts read-only credentials on its connection.'
	};
}
