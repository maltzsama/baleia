// Content browser (#113): cross-kind search, principals for access lists,
// folder moves and the metric list. Everything else reuses the savedQueries
// store (#40).
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type ContentSearchItem = components['schemas']['ContentSearchItem'];
export type WorkspacePrincipal = components['schemas']['WorkspacePrincipal'];
export type QueryFolder = components['schemas']['QueryFolder'];
export type SemanticMetric = components['schemas']['SemanticMetric'];
export type Space = components['schemas']['Space'];

export class ContentRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string
	) {
		super(message);
		this.name = 'ContentRequestError';
	}
}

function fail(error: unknown, status: number | undefined, fallback: string): never {
	const body = error as { code?: string } | undefined;
	throw new ContentRequestError(apiErrorMessage(error, fallback), status, body?.code);
}

export async function searchContent(
	workspaceId: string,
	q: string,
	options: { kind?: string; spaceId?: string; limit?: number } = {}
): Promise<ContentSearchItem[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/content/search', {
		params: {
			path: { id: workspaceId },
			query: { q, kind: options.kind, space_id: options.spaceId, limit: options.limit }
		}
	});
	if (error || !data) fail(error, response?.status, 'Could not search content');
	return data;
}

export async function listPrincipals(
	workspaceId: string,
	q: string,
	type?: 'user' | 'team'
): Promise<WorkspacePrincipal[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/principals', {
		params: { path: { id: workspaceId }, query: { q, type } }
	});
	if (error || !data) fail(error, response?.status, 'Could not search people and teams');
	return data;
}

export async function createFolder(
	workspaceId: string,
	body: { name: string; slug?: string; space_id: string; parent_id?: string | null }
): Promise<QueryFolder> {
	const { data, error, response } = await client.POST('/workspaces/{id}/folders', {
		params: { path: { id: workspaceId } },
		body
	});
	if (error || !data) fail(error, response?.status, 'Could not create the folder');
	return data;
}

export async function updateFolder(
	workspaceId: string,
	folderId: string,
	body: { name?: string; slug?: string; parent_id?: string | null; space_id?: string | null }
): Promise<QueryFolder> {
	const { data, error, response } = await client.PATCH('/workspaces/{id}/folders/{folder_id}', {
		params: { path: { id: workspaceId, folder_id: folderId } },
		body
	});
	if (error || !data) fail(error, response?.status, 'Could not update the folder');
	return data;
}

export async function deleteFolder(workspaceId: string, folderId: string): Promise<void> {
	const { error, response } = await client.DELETE('/workspaces/{id}/folders/{folder_id}', {
		params: { path: { id: workspaceId, folder_id: folderId } }
	});
	if (error) fail(error, response?.status, 'Could not delete the folder');
}

export async function listMetrics(workspaceId: string): Promise<SemanticMetric[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/semantic/metrics', {
		params: { path: { id: workspaceId } }
	});
	if (error || !data) fail(error, response?.status, 'Could not load metrics');
	return data;
}

/** The route a content item opens (#112 / #33). */
export function contentHref(item: { kind: string; id: string }): string {
	if (item.kind === 'saved_query') return `/sql/q/${item.id}`;
	if (item.kind === 'semantic_metric') return `/metrics/${item.id}`;
	return `/content?folder=${item.id}`;
}
