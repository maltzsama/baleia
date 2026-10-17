// Table and column documentation (#116): Markdown descriptions, tags,
// owners, certification and docs search.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type SemanticDoc = components['schemas']['SemanticDoc'];
export type SemanticDocSearchResult = components['schemas']['SemanticDocSearchResult'];
export type SemanticTag = components['schemas']['SemanticTag'];
export type MetadataColumn = components['schemas']['MetadataColumn'];

export class DocRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string,
		readonly params?: Record<string, unknown>
	) {
		super(message);
		this.name = 'DocRequestError';
	}
}

function fail(error: unknown, status: number | undefined, fallback: string): never {
	const body = error as
		{ code?: string; message?: string; params?: Record<string, unknown> } | undefined;
	throw new DocRequestError(
		body?.message ?? apiErrorMessage(error, fallback),
		status,
		body?.code,
		body?.params
	);
}

type Key = { catalogId: string; namespace: string[]; table: string; column?: string | null };

function query(key: Key) {
	return {
		catalog_id: key.catalogId,
		ns: key.namespace,
		table: key.table,
		column: key.column ?? undefined
	};
}

export async function listDocs(workspaceId: string, key: Key): Promise<SemanticDoc[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/semantic/docs', {
		params: { path: { id: workspaceId }, query: query(key) }
	});
	if (error || !data) fail(error, response?.status, 'Could not load the documentation');
	return data;
}

export async function upsertDoc(
	workspaceId: string,
	key: Key,
	body: {
		description: string;
		tags: string[];
		owner_user_id?: string | null;
		certified?: boolean;
		expected_updated_at?: string | null;
	}
): Promise<SemanticDoc> {
	const { data, error, response } = await client.PUT('/workspaces/{id}/semantic/docs', {
		params: { path: { id: workspaceId }, query: query(key) },
		body
	});
	if (error || !data) fail(error, response?.status, 'Could not save the documentation');
	return data;
}

export async function deleteDoc(workspaceId: string, key: Key): Promise<void> {
	const { error, response } = await client.DELETE('/workspaces/{id}/semantic/docs', {
		params: { path: { id: workspaceId }, query: query(key) }
	});
	if (error) fail(error, response?.status, 'Could not delete the documentation');
}

export async function searchDocs(
	workspaceId: string,
	params: { q: string; tag?: string; certified?: boolean; owner_id?: string; limit?: number }
): Promise<SemanticDocSearchResult[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/semantic/docs/search', {
		params: { path: { id: workspaceId }, query: params }
	});
	if (error || !data) fail(error, response?.status, 'Could not search the documentation');
	return data;
}

export async function listTags(workspaceId: string, q = ''): Promise<SemanticTag[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/semantic/tags', {
		params: { path: { id: workspaceId }, query: { q: q || undefined } }
	});
	if (error || !data) fail(error, response?.status, 'Could not load tags');
	return data;
}

/** Tags normalize to lower case and match the server rule. */
export function normalizeTags(value: string): string[] {
	return [
		...new Set(
			value
				.split(',')
				.map((tag) => tag.trim().toLowerCase())
				.filter(Boolean)
		)
	];
}

export function tagValid(tag: string): boolean {
	return /^[a-z0-9][a-z0-9_-]{0,39}$/.test(tag);
}
