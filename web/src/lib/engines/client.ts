// Engine and dialect reads for the workbench (#24). Nothing here hard-codes an
// engine name: the dialect, formatter language and CodeMirror base all come
// from the API.
import { apiErrorMessage, client } from '$lib/api/client';
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
import type { SqlLanguage } from 'sql-formatter';

export type Engine = components['schemas']['Engine'];
export type EngineDialect = components['schemas']['EngineDialect'];
export type EngineCapabilities = components['schemas']['EngineCapabilities'];
export type Dialect = components['schemas']['Dialect'];
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

export function clearEnginesCache(): void {
	enginesCache.clear();
	dialectCache.clear();
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

/** The sql-formatter language id, falling back to its generic `sql`. */
export function formatterLanguage(name: string | undefined | null): SqlLanguage {
	if (!name) return 'sql';
	return name as SqlLanguage;
}
