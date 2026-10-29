// Per-table Iceberg maintenance (#500): parameters, plans, runs and
// "analyze now". The server checks access and validates the parameters.
import { m } from '$lib/paraglide/messages';
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type MaintenancePolicy = components['schemas']['MaintenancePolicy'];
export type StoredMaintenancePolicy = components['schemas']['StoredMaintenancePolicy'];
export type MaintenanceSettings = components['schemas']['MaintenanceSettings'];
export type MaintenancePlan = components['schemas']['MaintenancePlan'];
export type MaintenancePlanStep = components['schemas']['MaintenancePlanStep'];
export type MaintenanceReason = components['schemas']['MaintenanceReason'];
export type MaintenanceOperation = components['schemas']['MaintenanceOperation'];
export type MaintenanceMode = components['schemas']['MaintenanceMode'];
export type TableMaintenance = components['schemas']['TableMaintenance'];
export type CatalogMaintenancePlanItem = components['schemas']['CatalogMaintenancePlanItem'];
export type MaintenanceRun = components['schemas']['MaintenanceRun'];
type FieldError = components['schemas']['FieldError'];

/** The operations in the order a round runs them. */
export const OPERATIONS: MaintenanceOperation[] = [
	'optimize',
	'optimize_manifests',
	'expire_snapshots',
	'remove_orphan_files'
];

export class MaintenanceRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string,
		readonly fields: FieldError[] = []
	) {
		super(message);
		this.name = 'MaintenanceRequestError';
	}
}

type Result<T> = { data?: T; error?: unknown; response?: Response };

function unwrap<T>(result: Result<T>, fallback: string): T {
	// 202 and 204 carry no body; any other success without one is a failure.
	const empty = result.response?.status === 202 || result.response?.status === 204;
	if (result.error || (result.data === undefined && !empty)) {
		const body = result.error as { code?: string; fields?: FieldError[] } | undefined;
		throw new MaintenanceRequestError(
			apiErrorMessage(result.error, fallback),
			result.response?.status,
			body?.code,
			body?.fields ?? []
		);
	}
	return result.data as T;
}

type Table = { catalogId: string; namespace: string; table: string };

const path = (t: Table) => ({
	params: { path: { catalog_id: t.catalogId, namespace: t.namespace, table: t.table } }
});

export async function getTableMaintenance(t: Table): Promise<TableMaintenance> {
	return unwrap(
		await client.GET('/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance', path(t)),
		m.maintenance_could_not_load()
	);
}

/** Creates the parameters, or replaces them at revision. */
export async function saveTableMaintenance(
	t: Table,
	policy: MaintenancePolicy,
	revision?: number
): Promise<StoredMaintenancePolicy> {
	return unwrap(
		await client.PUT('/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance', {
			params: {
				path: { catalog_id: t.catalogId, namespace: t.namespace, table: t.table },
				header: revision === undefined ? {} : { 'If-Match': String(revision) }
			},
			body: policy
		}),
		m.maintenance_could_not_save()
	);
}

export async function deleteTableMaintenance(t: Table): Promise<void> {
	unwrap(
		await client.DELETE('/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance', path(t)),
		m.maintenance_could_not_remove()
	);
}

export async function dryRunTableMaintenance(t: Table): Promise<MaintenancePlan> {
	return unwrap(
		await client.POST(
			'/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance/dry-run',
			path(t)
		),
		m.maintenance_could_not_plan()
	);
}

export async function runTableMaintenance(
	t: Table,
	operation: MaintenanceOperation | 'next_due'
): Promise<MaintenanceRun> {
	return unwrap(
		await client.POST('/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance/runs', {
			...path(t),
			body: { operation }
		}),
		m.maintenance_could_not_run()
	);
}

export async function analyzeTableHealth(t: Table): Promise<void> {
	unwrap(
		await client.POST(
			'/catalogs/{catalog_id}/tables/{namespace}/{table}/table-health/analyze',
			path(t)
		),
		m.maintenance_could_not_analyze()
	);
}

export async function analyzeCatalogTableHealth(catalogId: string): Promise<void> {
	unwrap(
		await client.POST('/catalogs/{catalog_id}/table-health/analyze', {
			params: { path: { catalog_id: catalogId } }
		}),
		m.maintenance_could_not_analyze()
	);
}

export async function getCatalogMaintenancePlan(
	catalogId: string
): Promise<CatalogMaintenancePlanItem[]> {
	return unwrap(
		await client.GET('/catalogs/{catalog_id}/maintenance-plan', {
			params: { path: { catalog_id: catalogId } }
		}),
		m.maintenance_could_not_load()
	);
}

/** A disabled operation with the defaults a new one starts from. */
export function emptySettings(): MaintenanceSettings {
	return {
		enabled: false,
		trigger: {},
		min_interval_seconds: 3600,
		max_interval_seconds: null,
		window: null,
		timeout_seconds: 1800,
		on_conflict: 'retry_with_backoff',
		params: {}
	};
}

/** New parameters: managed, every operation off. */
export function emptyPolicy(): MaintenancePolicy {
	return {
		mode: 'managed',
		writer_commit_interval_seconds: 60,
		optimize: emptySettings(),
		optimize_manifests: emptySettings(),
		expire_snapshots: emptySettings(),
		remove_orphan_files: emptySettings()
	};
}

/** The data explorer's query for a table (namespace segments joined by "."). */
export function dataTableQuery(catalogId: string, namespace: string, table: string): string {
	const params = new URLSearchParams({ catalog: catalogId });
	for (const segment of namespace.split('.')) params.append('ns', segment);
	params.set('table', table);
	return params.toString();
}
