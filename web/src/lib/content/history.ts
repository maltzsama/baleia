// Version history, trash and space bundles (#114).
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type ContentVersion = components['schemas']['ContentVersion'];
export type ContentVersionDetail = components['schemas']['ContentVersionDetail'];
export type TrashItem = components['schemas']['TrashItem'];
export type ImportReport = components['schemas']['ImportReport'];
export type ImportItem = components['schemas']['ImportItem'];
export type Bundle = components['schemas']['Bundle'];

export class ContentHistoryError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string
	) {
		super(message);
		this.name = 'ContentHistoryError';
	}
}

function fail(error: unknown, status: number | undefined, fallback: string): never {
	const body = error as { code?: string } | undefined;
	throw new ContentHistoryError(apiErrorMessage(error, fallback), status, body?.code);
}

function paths(kind: string) {
	if (kind === 'semantic_model') {
		return {
			list: '/workspaces/{id}/semantic/models/{model_id}/versions' as const,
			get: '/workspaces/{id}/semantic/models/{model_id}/versions/{version}' as const,
			restore: '/workspaces/{id}/semantic/models/{model_id}/versions/{version}/restore' as const,
			param: 'model_id' as const
		};
	}
	if (kind === 'semantic_metric') {
		return {
			list: '/workspaces/{id}/semantic/metrics/{metric_id}/versions' as const,
			get: '/workspaces/{id}/semantic/metrics/{metric_id}/versions/{version}' as const,
			restore: '/workspaces/{id}/semantic/metrics/{metric_id}/versions/{version}/restore' as const,
			param: 'metric_id' as const
		};
	}
	return {
		list: '/workspaces/{id}/queries/{query_id}/versions' as const,
		get: '/workspaces/{id}/queries/{query_id}/versions/{version}' as const,
		restore: '/workspaces/{id}/queries/{query_id}/versions/{version}/restore' as const,
		param: 'query_id' as const
	};
}

export async function listVersions(
	workspaceId: string,
	kind: string,
	entityId: string
): Promise<ContentVersion[]> {
	const p = paths(kind);
	const path = { id: workspaceId, [p.param]: entityId } as never;
	const { data, error, response } = await client.GET(p.list, { params: { path } });
	if (error || !data) fail(error, response?.status, 'Could not load the history');
	return data;
}

export async function getVersion(
	workspaceId: string,
	kind: string,
	entityId: string,
	version: number
): Promise<ContentVersionDetail> {
	const p = paths(kind);
	const path = { id: workspaceId, [p.param]: entityId, version } as never;
	const { data, error, response } = await client.GET(p.get, { params: { path } });
	if (error || !data) fail(error, response?.status, 'Could not load the version');
	return data;
}

export async function restoreVersion(
	workspaceId: string,
	kind: string,
	entityId: string,
	version: number,
	body: { new_name?: string; space_id?: string } = {}
): Promise<void> {
	const p = paths(kind);
	const path = { id: workspaceId, [p.param]: entityId, version } as never;
	const { error, response } = await client.POST(p.restore, { params: { path }, body });
	if (error) fail(error, response?.status, 'Could not restore the version');
}

export async function listTrash(
	workspaceId: string,
	entityType?: 'saved_query' | 'semantic_metric' | 'semantic_model'
): Promise<TrashItem[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/trash', {
		params: { path: { id: workspaceId }, query: { entity_type: entityType } }
	});
	if (error || !data) fail(error, response?.status, 'Could not load the trash');
	return data;
}

export async function exportSpace(workspaceId: string, spaceId: string): Promise<Bundle> {
	const { data, error, response } = await client.GET('/workspaces/{id}/spaces/{space_id}/export', {
		params: { path: { id: workspaceId, space_id: spaceId } }
	});
	if (error || !data) fail(error, response?.status, 'Could not export the space');
	return data;
}

export async function importSpace(
	workspaceId: string,
	spaceId: string,
	mode: 'dry_run' | 'apply',
	bundle: unknown
): Promise<{ report?: ImportReport; status: number }> {
	const { data, error, response } = await client.POST('/workspaces/{id}/spaces/{space_id}/import', {
		params: { path: { id: workspaceId, space_id: spaceId }, query: { mode } },
		body: bundle as Bundle
	});
	if (data) return { report: data, status: response?.status ?? 200 };
	if (error) {
		const body = error as { items?: ImportReport['items']; mode?: string; ok?: boolean };
		if (body.items) {
			return {
				report: { mode: mode, ok: false, items: body.items },
				status: response?.status ?? 422
			};
		}
		fail(error, response?.status, 'Could not import the bundle');
	}
	return { status: 200 };
}

/** Pretty JSON with sorted keys, so key order never appears as a change. */
export function canonicalJSON(value: unknown): string {
	if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(',')}]`;
	if (value && typeof value === 'object') {
		const entries = Object.entries(value as Record<string, unknown>).sort(([a], [b]) =>
			a.localeCompare(b)
		);
		return `{${entries.map(([key, val]) => `${JSON.stringify(key)}:${canonicalJSON(val)}`).join(',')}}`;
	}
	return JSON.stringify(value ?? null);
}

export function snapshotSQL(snapshot: Record<string, unknown> | undefined): string {
	if (!snapshot) return '';
	return typeof snapshot.sql === 'string' ? snapshot.sql : '';
}

export function snapshotMetricJSON(snapshot: Record<string, unknown> | undefined): string {
	if (!snapshot) return '';
	return canonicalJSON(snapshot.definition ?? snapshot);
}
