// Typed calls of the workspace search and its glossary (#943). The search is
// one ranked list over tables, columns, docs, saved queries, metrics, models
// and folders; the API merges what each surface finds and says how many of
// each kind matched.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';
import { m } from '$lib/paraglide/messages';

export type WorkspaceSearchItem = components['schemas']['WorkspaceSearchItem'];
export type WorkspaceSearchKind = components['schemas']['WorkspaceSearchKind'];
export type WorkspaceSearchFacet = components['schemas']['WorkspaceSearchFacet'];
export type WorkspaceSearchResponse = components['schemas']['WorkspaceSearchResponse'];
export type MetadataSearchResult = components['schemas']['MetadataSearchResult'];
export type SearchGlossary = components['schemas']['SearchGlossary'];
export type SearchGlossaryEntry = components['schemas']['SearchGlossaryEntry'];

export class SearchRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number
	) {
		super(message);
		this.name = 'SearchRequestError';
	}
}

function fail(error: unknown, status: number | undefined, fallback: string): never {
	throw new SearchRequestError(apiErrorMessage(error, fallback), status);
}

/**
 * The best matches for `q`, best first, with a count per kind. Not cached.
 * The workspace glossary applies on the server.
 */
export function searchWorkspace(
	workspaceId: string,
	q: string,
	options: {
		kinds?: WorkspaceSearchKind[];
		catalogId?: string;
		spaceId?: string;
		limit?: number;
		signal?: AbortSignal;
	} = {}
): Promise<WorkspaceSearchResponse> {
	return client
		.GET('/workspaces/{id}/search', {
			params: {
				path: { id: workspaceId },
				query: {
					q,
					...(options.kinds?.length ? { kind: options.kinds } : {}),
					...(options.catalogId ? { catalog_id: options.catalogId } : {}),
					...(options.spaceId ? { space_id: options.spaceId } : {}),
					...(options.limit ? { limit: options.limit } : {})
				}
			},
			signal: options.signal
		})
		.then(({ data, error, response }) => {
			if (error || !data) fail(error, response?.status, m.search_failed());
			return data;
		});
}

/** The glossary entries of a workspace, and what the caller may do with them. */
export function loadGlossary(workspaceId: string): Promise<SearchGlossary> {
	return client
		.GET('/workspaces/{id}/search/glossary', { params: { path: { id: workspaceId } } })
		.then(({ data, error, response }) => {
			if (error || !data) fail(error, response?.status, m.search_glossary_load_failed());
			return data;
		});
}

/** Adds a set of synonyms; a term that already has an entry is a 409. */
export function addGlossaryEntry(
	workspaceId: string,
	term: string,
	synonyms: string[]
): Promise<SearchGlossaryEntry> {
	return client
		.POST('/workspaces/{id}/search/glossary', {
			params: { path: { id: workspaceId } },
			body: { term, synonyms }
		})
		.then(({ data, error, response }) => {
			if (error || !data) fail(error, response?.status, m.search_glossary_add_failed());
			return data;
		});
}

/** Removes a glossary entry. */
export function removeGlossaryEntry(workspaceId: string, entryId: string): Promise<void> {
	return client
		.DELETE('/workspaces/{id}/search/glossary/{entry_id}', {
			params: { path: { id: workspaceId, entry_id: entryId } }
		})
		.then(({ error, response }) => {
			if (error) fail(error, response?.status, m.search_glossary_remove_failed());
		});
}

/**
 * The tables and columns the workspace search finds for `q`, as the explorer
 * lists them, and the glossary synonyms it also looked for. The explorer's
 * tag, certified and namespace filters are the metadata search's: it keeps
 * using that one for them.
 */
export async function searchTablesAndColumns(
	workspaceId: string,
	q: string,
	options: { kind?: 'table' | 'column' | 'all'; limit?: number; signal?: AbortSignal } = {}
): Promise<{ results: MetadataSearchResult[]; expandedWith: string[] }> {
	const kinds: WorkspaceSearchKind[] =
		options.kind === 'table'
			? ['table']
			: options.kind === 'column'
				? ['column']
				: ['table', 'column'];
	const found = await searchWorkspace(workspaceId, q, {
		kinds,
		limit: options.limit,
		signal: options.signal
	});
	return {
		results: found.items.flatMap((item) => (item.metadata ? [item.metadata] : [])),
		expandedWith: found.expanded_with
	};
}
