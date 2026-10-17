// Semantic metrics v1 (#33/#115). The browser never builds SQL for a metric:
// render and validate always go through the API.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type SemanticMetric = components['schemas']['SemanticMetric'];
export type SemanticMetricCreate = components['schemas']['SemanticMetricCreate'];
export type SemanticMetricUpdate = components['schemas']['SemanticMetricUpdate'];
export type MetricDefinition = {
	type: string;
	aggregation?: string;
	column?: string | null;
	filters?: { column: string; op: string; values?: unknown[] }[];
	time_column?: string | null;
	time_grains?: string[];
	format?: { type: string; decimals: number } | null;
	custom_sql?: string | null;
};
export type TableDetail = components['schemas']['MetadataTableDetail'];
export type MetadataColumn = components['schemas']['MetadataColumn'];
export type SearchResult = components['schemas']['MetadataSearchResult'];

export class MetricRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string,
		readonly params?: Record<string, unknown>
	) {
		super(message);
		this.name = 'MetricRequestError';
	}
}

function fail(error: unknown, status: number | undefined, fallback: string): never {
	const body = error as
		{ code?: string; message?: string; params?: Record<string, unknown> } | undefined;
	throw new MetricRequestError(
		body?.message ?? apiErrorMessage(error, fallback),
		status,
		body?.code,
		body?.params
	);
}

export async function listMetrics(
	workspaceId: string,
	params: {
		q?: string;
		space_id?: string;
		catalog_id?: string;
		certified?: boolean;
		owner_id?: string;
	} = {}
): Promise<SemanticMetric[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/semantic/metrics', {
		params: { path: { id: workspaceId }, query: params }
	});
	if (error || !data) fail(error, response?.status, 'Could not load metrics');
	return data;
}

export async function getMetric(workspaceId: string, metricId: string): Promise<SemanticMetric> {
	const { data, error, response } = await client.GET(
		'/workspaces/{id}/semantic/metrics/{metric_id}',
		{
			params: { path: { id: workspaceId, metric_id: metricId } }
		}
	);
	if (error || !data) fail(error, response?.status, 'Could not load the metric');
	return data;
}

export async function createMetric(
	workspaceId: string,
	body: SemanticMetricCreate
): Promise<SemanticMetric> {
	const { data, error, response } = await client.POST('/workspaces/{id}/semantic/metrics', {
		params: { path: { id: workspaceId } },
		body
	});
	if (error || !data) fail(error, response?.status, 'Could not create the metric');
	return data;
}

export async function updateMetric(
	workspaceId: string,
	metricId: string,
	body: SemanticMetricUpdate
): Promise<SemanticMetric> {
	const { data, error, response } = await client.PATCH(
		'/workspaces/{id}/semantic/metrics/{metric_id}',
		{ params: { path: { id: workspaceId, metric_id: metricId } }, body }
	);
	if (error || !data) fail(error, response?.status, 'Could not update the metric');
	return data;
}

export async function deleteMetric(workspaceId: string, metricId: string): Promise<void> {
	const { error, response } = await client.DELETE('/workspaces/{id}/semantic/metrics/{metric_id}', {
		params: { path: { id: workspaceId, metric_id: metricId } }
	});
	if (error) fail(error, response?.status, 'Could not delete the metric');
}

export type RenderBody = {
	catalog_id: string;
	namespace: string[];
	table: string;
	definition: MetricDefinition;
	binding_id?: string | null;
	grain?: string | null;
};

export async function renderMetric(workspaceId: string, body: RenderBody): Promise<string> {
	const { data, error, response } = await client.POST('/workspaces/{id}/semantic/metrics/render', {
		params: { path: { id: workspaceId } },
		body: body as unknown as components['schemas']['SemanticMetricRenderRequest']
	});
	if (error || !data) fail(error, response?.status, 'Could not render the metric');
	return data.sql;
}

export async function validateMetric(workspaceId: string, body: RenderBody): Promise<void> {
	const { error, response } = await client.POST('/workspaces/{id}/semantic/metrics/validate', {
		params: { path: { id: workspaceId } },
		body: body as unknown as components['schemas']['SemanticMetricRenderRequest']
	});
	if (error) fail(error, response?.status, 'The engine rejected the metric');
}

export async function metricSQL(
	workspaceId: string,
	metricId: string,
	params: {
		binding_id?: string;
		grain?: 'day' | 'hour' | 'minute' | 'month' | 'second' | 'year' | 'quarter' | 'week';
	}
): Promise<{ sql: string; portable: boolean }> {
	const { data, error, response } = await client.GET(
		'/workspaces/{id}/semantic/metrics/{metric_id}/sql',
		{ params: { path: { id: workspaceId, metric_id: metricId }, query: params } }
	);
	if (error || !data) fail(error, response?.status, 'Could not render the metric');
	return data;
}

export async function searchTables(workspaceId: string, q: string): Promise<SearchResult[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/metadata/search', {
		params: { path: { id: workspaceId }, query: { q, kind: 'table' } }
	});
	if (error || !data) fail(error, response?.status, 'Could not search tables');
	return data;
}

export async function getTable(workspaceId: string, tableId: string): Promise<TableDetail> {
	const { data, error, response } = await client.GET(
		'/workspaces/{id}/metadata/tables/{table_id}',
		{ params: { path: { id: workspaceId, table_id: tableId } } }
	);
	if (error || !data) fail(error, response?.status, 'Could not load the table');
	return data;
}

/** Aggregations that make sense for a column's portable type (#115). */
export function aggregationsFor(column: MetadataColumn | undefined): string[] {
	if (!column) return ['count'];
	const logical = column.logical_type ?? 'other';
	const numeric = ['int64', 'decimal', 'float64'];
	if (numeric.includes(logical)) return ['sum', 'avg', 'min', 'max', 'count_distinct', 'count'];
	if (
		logical === 'date' ||
		logical === 'timestamp' ||
		logical === 'timestamptz' ||
		logical === 'string'
	) {
		return ['min', 'max', 'count_distinct', 'count'];
	}
	return ['count_distinct', 'count'];
}

export function isTimeColumn(column: MetadataColumn): boolean {
	return (
		column.logical_type === 'date' ||
		column.logical_type === 'timestamp' ||
		column.logical_type === 'timestamptz'
	);
}

export function isNumericColumn(column: MetadataColumn): boolean {
	return ['int64', 'decimal', 'float64'].includes(column.logical_type ?? '');
}
