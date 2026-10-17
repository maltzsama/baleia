// Autocomplete helpers for the workbench (#24). A tiny statement scanner maps
// `alias.` to the table in the statement's FROM/JOIN clauses; the columns come
// from the stored metadata snapshot, never from the engine.
import { loadTable, searchMetadata, type MetadataColumn } from '$lib/metadata/client';

export function stripQuotes(value: string): string {
	return value.replace(/^[`"[]/, '').replace(/[`"\]]$/, '');
}

/** Maps an alias to its table name from the statement's FROM/JOIN clauses. */
export function tableForAlias(statement: string, alias: string): string {
	const pattern =
		/\b(?:from|join)\s+([`"[]?[\w$.]+[`"\]]?)(?:\s+(?:as\s+)?([`"[]?[\w$]+[`"\]]?))?/gi;
	for (const match of statement.matchAll(pattern)) {
		const qualified = stripQuotes(match[1]);
		const table = qualified.split('.').pop() ?? qualified;
		const declared = match[2] ? stripQuotes(match[2]) : table;
		if (declared.toLowerCase() === alias.toLowerCase()) return table;
	}
	return alias;
}

const columnCache = new Map<string, MetadataColumn[]>();

/** Drops cached table columns. Tests call it between cases. */
export function clearCompletionCache(): void {
	columnCache.clear();
}

/**
 * Columns of the table an alias refers to. Exact table-name matches win over
 * fuzzy search hits; the result is cached per table name.
 */
export async function resolveAliasColumns(
	workspaceId: string,
	statement: string,
	alias: string,
	signal?: AbortSignal
): Promise<MetadataColumn[]> {
	const table = tableForAlias(statement, alias);
	const cached = columnCache.get(table);
	if (cached) return cached;
	const results = await searchMetadata(workspaceId, table, { kind: 'table', limit: 20, signal });
	const hit = results.find((result) => result.table === table) ?? results[0];
	if (!hit) return [];
	const detail = await loadTable(workspaceId, hit.table_id);
	columnCache.set(table, detail.columns);
	return detail.columns;
}
