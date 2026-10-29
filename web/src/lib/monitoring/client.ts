// Typed engine-aware monitoring calls (#117). The server decides which
// engines, catalogs and queries the caller may see.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type MonitoringEngine = components['schemas']['MonitoringEngine'];
export type ClusterSeries = components['schemas']['ClusterSeries'];
export type ClusterPoint = components['schemas']['ClusterPoint'];
export type MonitoringQuery = components['schemas']['MonitoringQuery'];
export type MonitoringQueryPage = components['schemas']['MonitoringQueryPage'];
export type MonitoringQueryDetail = components['schemas']['MonitoringQueryDetail'];
export type TableHealth = components['schemas']['TableHealth'];
export type MaintenanceRun = components['schemas']['MaintenanceRun'];
export type CreateMaintenanceRequest = components['schemas']['CreateMaintenanceRequest'];

export class MonitoringRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string
	) {
		super(message);
		this.name = 'MonitoringRequestError';
	}
}

type Result<T> = { data?: T; error?: unknown; response?: Response };

function fail<T>(result: Result<T>, fallback: string): T {
	if (result.error || result.data === undefined) {
		const body = result.error as { code?: string } | undefined;
		throw new MonitoringRequestError(
			apiErrorMessage(result.error, fallback),
			result.response?.status,
			body?.code
		);
	}
	return result.data;
}

export async function listMonitoringEngines(): Promise<MonitoringEngine[]> {
	return fail(await client.GET('/monitoring/engines'), 'Could not load monitoring engines');
}

export async function getCluster(engineId: string, window: string): Promise<ClusterSeries> {
	return fail(
		await client.GET('/engines/{id}/monitoring/cluster', {
			params: { path: { id: engineId }, query: { window } }
		}),
		'Could not load the cluster'
	);
}

export async function listQueries(
	engineId: string,
	params: { state?: string; scope: string; q?: string; limit?: number; offset?: number }
): Promise<MonitoringQueryPage> {
	return fail(
		await client.GET('/engines/{id}/monitoring/queries', {
			params: { path: { id: engineId }, query: params }
		}),
		'Could not load running queries'
	);
}

export async function getQuery(engineId: string, queryId: string): Promise<MonitoringQueryDetail> {
	return fail(
		await client.GET('/engines/{id}/monitoring/queries/{query_id}', {
			params: { path: { id: engineId, query_id: queryId } }
		}),
		'Could not load the query'
	);
}

export async function killQuery(engineId: string, queryId: string): Promise<void> {
	const result = await client.POST('/engines/{id}/monitoring/queries/{query_id}', {
		params: { path: { id: engineId, query_id: queryId } }
	});
	if (result.error) fail(result, 'Could not cancel the query');
}

export async function listHealth(
	catalogId: string,
	params: { ns?: string; needs?: string; maxScore?: number; limit?: number; offset?: number }
): Promise<TableHealth[]> {
	return fail(
		await client.GET('/catalogs/{catalog_id}/table-health', {
			params: { path: { catalog_id: catalogId }, query: params }
		}),
		'Could not load table health'
	);
}

export async function createMaintenance(
	catalogId: string,
	body: CreateMaintenanceRequest
): Promise<MaintenanceRun> {
	return fail(
		await client.POST('/catalogs/{catalog_id}/maintenance', {
			params: { path: { catalog_id: catalogId } },
			body
		}),
		'Could not start maintenance'
	);
}

export type RunFilter = {
	trigger?: 'manual' | 'automatic' | 'dry_run';
	operation?: 'optimize' | 'optimize_manifests' | 'expire_snapshots' | 'remove_orphan_files';
};

export async function listMaintenanceRuns(
	catalogId: string,
	filter: RunFilter = {}
): Promise<MaintenanceRun[]> {
	return fail(
		await client.GET('/catalogs/{catalog_id}/maintenance-runs', {
			params: { path: { catalog_id: catalogId }, query: filter }
		}),
		'Could not load maintenance runs'
	);
}
