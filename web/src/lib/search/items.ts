// How a workspace search result reads and where it goes (#943).
import { m } from '$lib/paraglide/messages';
import { kindEntry } from '$lib/content/kinds';
import type { WorkspaceSearchItem, WorkspaceSearchKind } from './client';

/** The kind's name in the current locale. */
export function searchKindLabel(kind: WorkspaceSearchKind): string {
	switch (kind) {
		case 'table':
			return m.search_kind_table();
		case 'column':
			return m.search_kind_column();
		case 'doc':
			return m.search_kind_doc();
		case 'saved_query':
			return m.search_kind_saved_query();
		case 'semantic_metric':
			return m.search_kind_semantic_metric();
		case 'semantic_model':
			return m.search_kind_semantic_model();
		case 'folder':
			return m.search_kind_folder();
	}
}

/** The kinds that live in the content tree: saved content and its folders. */
export function isContentKind(kind: WorkspaceSearchKind): boolean {
	return (
		kind === 'saved_query' ||
		kind === 'semantic_metric' ||
		kind === 'semantic_model' ||
		kind === 'folder'
	);
}

/** The dotted, qualified name of a table or column hit, for the editor. */
export function qualifiedName(item: WorkspaceSearchItem): string {
	const hit = item.metadata;
	if (!hit) return item.title;
	const table = [...hit.namespace, hit.table].join('.');
	return hit.column ? `${table}.${hit.column}` : table;
}

/** Where a content or doc hit opens, by the route its kind owns. */
export function searchItemHref(item: WorkspaceSearchItem): string | null {
	if (item.content) return kindEntry(item.kind).href(item.content.id);
	if (item.doc) {
		const params = new URLSearchParams({ catalog: item.doc.catalog_id });
		for (const level of item.doc.namespace) params.append('ns', level);
		params.set('table', item.doc.table);
		return `/data?${params}`;
	}
	if (item.metadata) {
		const params = new URLSearchParams({ catalog: item.metadata.catalog_id });
		for (const level of item.metadata.namespace) params.append('ns', level);
		params.set('table', item.metadata.table);
		if (item.metadata.column) params.set('column', item.metadata.column);
		return `/data?${params}`;
	}
	return null;
}
