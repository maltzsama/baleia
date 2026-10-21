// Typed reads of the persisted metadata snapshot (#18/#23).
//
// Browsing never calls an engine: every function here hits `/metadata/*`.
// Catalogs, namespaces and tables are cached for the session so expanding a
// node twice (or opening it in the explorer and the workbench) issues one
// request; search is debounced by the caller and never cached.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type MetadataCatalog = components['schemas']['MetadataCatalog'];
export type MetadataCatalogBinding = components['schemas']['MetadataCatalogBinding'];
export type MetadataFreshness = components['schemas']['MetadataFreshness'];
export type MetadataNamespaceList = components['schemas']['MetadataNamespaceList'];
export type MetadataTableSummary = components['schemas']['MetadataTableSummary'];
export type MetadataTablePage = components['schemas']['MetadataTablePage'];
export type MetadataTableDetail = components['schemas']['MetadataTableDetail'];
export type MetadataColumn = components['schemas']['MetadataColumn'];
export type MetadataSearchResult = components['schemas']['MetadataSearchResult'];

/** A table the explorer selected, independent of the API's table detail. */
export type TableRef = {
	tableId: string;
	catalogId: string;
	catalogName: string;
	namespace: string[];
	tableName: string;
	bindings: MetadataCatalogBinding[];
};

export class MetadataRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number
	) {
		super(message);
		this.name = 'MetadataRequestError';
	}
}

function fail(error: unknown, status: number | undefined, fallback: string): never {
	throw new MetadataRequestError(apiErrorMessage(error, fallback), status);
}

// Promise caches: a cache hit is the in-flight request too, so two components
// expanding the same node share one round trip. Each cache is bounded so a long
// session (or a very large catalog) cannot grow memory without limit (#236).
type Cache<T> = Map<string, Promise<T>>;

const catalogsCache: Cache<MetadataCatalog[]> = new Map();
const namespacesCache: Cache<MetadataNamespaceList> = new Map();
const tablesCache: Cache<MetadataTablePage> = new Map();
const tableCache: Cache<MetadataTableDetail> = new Map();

/** Upper bound per cache; the oldest (least-recently-set) entry is evicted. */
const CACHE_MAX = 1000;

/** Drops every cached read. Tests call it between cases. */
export function clearMetadataCache(): void {
	catalogsCache.clear();
	namespacesCache.clear();
	tablesCache.clear();
	tableCache.clear();
}

function cached<T>(cache: Cache<T>, key: string, load: () => Promise<T>): Promise<T> {
	const hit = cache.get(key);
	if (hit) return hit;
	const pending = load().catch((error) => {
		// A failed read must not poison the cache: retry hits the API again.
		cache.delete(key);
		throw error;
	});
	cache.set(key, pending);
	// Bound the session cache (FIFO by insertion): drop the oldest entries so a
	// long-lived tab cannot grow memory without limit (#236).
	while (cache.size > CACHE_MAX) {
		const oldest = cache.keys().next().value;
		if (oldest === undefined) break;
		cache.delete(oldest);
	}
	return pending;
}

function nsKey(namespace: string[]): string {
	return namespace.join('\u0000');
}

/** Every catalog granted to the workspace, with its bindings and freshness. */
export function loadCatalogs(workspaceId: string): Promise<MetadataCatalog[]> {
	return cached(catalogsCache, workspaceId, async () => {
		const { data, error, response } = await client.GET('/workspaces/{id}/metadata/catalogs', {
			params: { path: { id: workspaceId } }
		});
		if (error || !data) fail(error, response?.status, 'Failed to load catalogs');
		return data;
	});
}

/** Direct child namespaces; an empty parent lists the top level. */
export function loadNamespaces(
	workspaceId: string,
	catalogId: string,
	parent: string[] = []
): Promise<MetadataNamespaceList> {
	const key = `${workspaceId}:${catalogId}:${nsKey(parent)}`;
	return cached(namespacesCache, key, async () => {
		const { data, error, response } = await client.GET(
			'/workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces',
			{
				params: {
					path: { id: workspaceId, catalog_id: catalogId },
					query: parent.length ? { parent } : {}
				}
			}
		);
		if (error || !data) fail(error, response?.status, 'Failed to load namespaces');
		return data;
	});
}

/** One page of tables in a namespace (100 by default, `cursor` follows on). */
export function loadTables(
	workspaceId: string,
	catalogId: string,
	namespace: string[],
	cursor?: string,
	limit = 100
): Promise<MetadataTablePage> {
	const key = `${workspaceId}:${catalogId}:${nsKey(namespace)}:${cursor ?? ''}:${limit}`;
	return cached(tablesCache, key, async () => {
		const { data, error, response } = await client.GET(
			'/workspaces/{id}/metadata/catalogs/{catalog_id}/tables',
			{
				params: {
					path: { id: workspaceId, catalog_id: catalogId },
					query: {
						...(namespace.length ? { ns: namespace } : {}),
						...(cursor ? { cursor } : {}),
						limit
					}
				}
			}
		);
		if (error || !data) fail(error, response?.status, 'Failed to load tables');
		return data;
	});
}

/** A table rendered for one binding (types, qualified name, preview SQL). */
export function loadTable(
	workspaceId: string,
	tableId: string,
	bindingId?: string
): Promise<MetadataTableDetail> {
	const key = `${workspaceId}:${tableId}:${bindingId ?? ''}`;
	return cached(tableCache, key, async () => {
		const { data, error, response } = await client.GET(
			'/workspaces/{id}/metadata/tables/{table_id}',
			{
				params: {
					path: { id: workspaceId, table_id: tableId },
					query: bindingId ? { binding_id: bindingId } : {}
				}
			}
		);
		if (error || !data) fail(error, response?.status, 'Failed to load the table');
		return data;
	});
}

/** Tables and columns whose names match `q`, best first. Not cached. */
export function searchMetadata(
	workspaceId: string,
	q: string,
	options: {
		kind?: 'table' | 'column' | 'all';
		/** Only this catalog; every catalog the workspace may use otherwise. */
		catalogId?: string;
		limit?: number;
		signal?: AbortSignal;
	} = {}
): Promise<MetadataSearchResult[]> {
	return client
		.GET('/workspaces/{id}/metadata/search', {
			params: {
				path: { id: workspaceId },
				query: {
					q,
					...(options.kind ? { kind: options.kind } : {}),
					...(options.catalogId ? { catalog_id: options.catalogId } : {}),
					...(options.limit ? { limit: options.limit } : {})
				}
			},
			signal: options.signal
		})
		.then(({ data, error, response }) => {
			if (error || !data) fail(error, response?.status, 'Search failed');
			return data;
		});
}
