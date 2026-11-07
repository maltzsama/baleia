// Engine and dialect reads for the workbench (#24). Nothing here hard-codes an
// engine name: the dialect, formatter language and CodeMirror base all come
// from the API.
import { apiErrorMessage, client } from '$lib/api/client';
import { m } from '$lib/paraglide/messages';
import type { components } from '$lib/api/schema';
import {
	Cassandra,
	MariaSQL,
	MSSQL,
	MySQL,
	PLSQL,
	PostgreSQL,
	SQLite,
	StandardSQL,
	type SQLDialect
} from '@codemirror/lang-sql';

export type Engine = components['schemas']['Engine'];
export type EngineDialect = components['schemas']['EngineDialect'];
export type EngineCapabilities = components['schemas']['EngineCapabilities'];
export type Dialect = components['schemas']['Dialect'];
export type DialectFunction = components['schemas']['DialectFunction'];
export type WorkspaceTarget = components['schemas']['WorkspaceTarget'];

export class EngineRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number
	) {
		super(message);
		this.name = 'EngineRequestError';
	}
}

const enginesCache = new Map<string, Promise<Engine[]>>();
const dialectCache = new Map<string, Promise<Dialect>>();
const functionsCache = new Map<string, Promise<DialectFunction[]>>();
const bindingFunctionsCache = new Map<
	string,
	{ at: number; promise: Promise<DialectFunction[] | null> }
>();
// An engine's catalog changes rarely; five minutes keeps the editor from
// missing functions added after the first lookup without hammering the API.
const BINDING_FUNCTIONS_TTL = 5 * 60_000;

export function clearEnginesCache(): void {
	enginesCache.clear();
	dialectCache.clear();
	functionsCache.clear();
	bindingFunctionsCache.clear();
}

function fail(error: unknown, status: number | undefined, fallback: string): never {
	throw new EngineRequestError(apiErrorMessage(error, fallback), status);
}

/** Engines the caller may see, ordered by name. */
export function loadEngines(): Promise<Engine[]> {
	const key = 'engines';
	const hit = enginesCache.get(key);
	if (hit) return hit;
	const pending = client
		.GET('/engines')
		.then(({ data, error, response }) => {
			if (error || !data) fail(error, response?.status, 'Failed to load engines');
			return data;
		})
		.catch((error) => {
			enginesCache.delete(key);
			throw error;
		});
	enginesCache.set(key, pending);
	return pending;
}

/** A dialect with its keyword and function lists, cached per name. */
/** The dialect's function reference, with signatures where committed (B2, #531). */
export function loadFunctions(name: string): Promise<DialectFunction[]> {
	const hit = functionsCache.get(name);
	if (hit) return hit;
	const pending = client
		.GET('/dialects/{name}/functions', { params: { path: { name } } })
		.then(({ data, error, response }) => {
			if (error || !data) fail(error, response?.status, m.functions_panel_failed());
			return data;
		})
		.catch((error) => {
			functionsCache.delete(name);
			throw error;
		});
	functionsCache.set(name, pending);
	return pending;
}

/**
 * The connected engine's own function catalog for a binding, introspected at
 * runtime and cached per binding (#788). Resolves to null when it is
 * unavailable (an engine with no catalog, or the engine is down), so callers
 * fall back to the static reference from {@link loadFunctions}.
 */
export function loadBindingFunctions(
	workspaceId: string,
	catalogId: string,
	bindingId: string
): Promise<DialectFunction[] | null> {
	const key = `${workspaceId}:${catalogId}:${bindingId}`;
	const hit = bindingFunctionsCache.get(key);
	if (hit && Date.now() - hit.at < BINDING_FUNCTIONS_TTL) return hit.promise;
	const pending = client
		.GET('/workspaces/{id}/metadata/functions', {
			params: {
				path: { id: workspaceId },
				query: { catalog_id: catalogId, binding_id: bindingId || undefined }
			}
		})
		.then(({ data, error }) => {
			if (error || !data) {
				// Evict a failed lookup so a recovered engine is retried.
				bindingFunctionsCache.delete(key);
				return null;
			}
			return data;
		})
		.catch(() => {
			bindingFunctionsCache.delete(key);
			return null;
		});
	bindingFunctionsCache.set(key, { at: Date.now(), promise: pending });
	return pending;
}

export function loadDialect(name: string): Promise<Dialect> {
	const hit = dialectCache.get(name);
	if (hit) return hit;
	const pending = client
		.GET('/dialects/{name}', { params: { path: { name } } })
		.then(({ data, error, response }) => {
			if (error || !data) fail(error, response?.status, 'Failed to load the dialect');
			return data;
		})
		.catch((error) => {
			dialectCache.delete(name);
			throw error;
		});
	dialectCache.set(name, pending);
	return pending;
}

// `codemirror_base` is the @codemirror/lang-sql dialect name reported by the
// engine adapter. Unknown values fall back to StandardSQL.
const CODEMIRROR_DIALECTS: Record<string, SQLDialect> = {
	StandardSQL,
	PostgreSQL,
	MySQL,
	MariaSQL,
	MSSQL,
	PLSQL,
	SQLite,
	Cassandra
};

export function codemirrorDialect(name: string | undefined | null): SQLDialect {
	if (!name) return StandardSQL;
	return CODEMIRROR_DIALECTS[name] ?? StandardSQL;
}
