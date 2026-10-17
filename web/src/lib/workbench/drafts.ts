// Workbench drafts (#112): tabs persist per user × workspace in IndexedDB.
// SQL over 1 MiB is not persisted; storage failures fall back to memory.
import { del, get, keys, set } from 'idb-keyval';
import type { EditorTab } from '$lib/stores/editor.svelte';

export const MAX_DRAFT_TABS = 30;
export const MAX_DRAFT_SQL = 1 << 20;

const PREFIX = 'baleia.tabs.';

export type PersistedTab = {
	id: string;
	name: string;
	sql: string;
	savedRef: EditorTab['savedRef'];
	bindingId: string;
	schema: string;
	readOnly: boolean;
	savedSql: string;
	savedBindingId: string;
	savedSchema: string;
};

function key(userId: string, workspaceId: string): string {
	return `${PREFIX}${userId}.${workspaceId}`;
}

function toPersisted(tab: EditorTab): PersistedTab {
	return {
		id: tab.id,
		name: tab.name,
		sql: tab.sql,
		savedRef: tab.savedRef,
		bindingId: tab.bindingId,
		schema: tab.schema,
		readOnly: tab.readOnly,
		savedSql: tab.savedSql,
		savedBindingId: tab.savedBindingId,
		savedSchema: tab.savedSchema
	};
}

/** Drafts for a user and workspace, or [] when storage is unavailable. */
export async function loadDrafts(userId: string, workspaceId: string): Promise<PersistedTab[]> {
	try {
		const raw = await get<PersistedTab[]>(key(userId, workspaceId));
		if (!Array.isArray(raw)) return [];
		return raw
			.filter((tab) => tab && typeof tab.id === 'string' && typeof tab.sql === 'string')
			.slice(0, MAX_DRAFT_TABS);
	} catch {
		return [];
	}
}

/** Persists at most 30 tabs with SQL ≤ 1 MiB. Never throws. */
export async function saveDrafts(
	userId: string,
	workspaceId: string,
	tabs: EditorTab[]
): Promise<void> {
	try {
		const persisted = tabs
			.slice(0, MAX_DRAFT_TABS)
			.filter((tab) => tab.sql.length <= MAX_DRAFT_SQL)
			.map(toPersisted);
		await set(key(userId, workspaceId), persisted);
	} catch {
		// Private mode or a full quota: the editor keeps working in memory.
	}
}

/** Removes every draft of a user (explicit logout). */
export async function clearDrafts(userId: string): Promise<void> {
	try {
		const stored = await keys();
		const prefix = `${PREFIX}${userId}.`;
		await Promise.all(
			stored
				.filter((item): item is string => typeof item === 'string' && item.startsWith(prefix))
				.map((item) => del(item))
		);
	} catch {
		// Nothing to clear.
	}
}
