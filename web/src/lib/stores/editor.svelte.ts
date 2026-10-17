// Typed workbench state (#29). Components read and mutate tabs here instead of
// exchanging `window` CustomEvents.
import type { components } from '$lib/api/schema';

export type SavedQuery = components['schemas']['SavedQuery'];
export type SavedQueryRef = Pick<SavedQuery, 'id' | 'name' | 'sql'>;

export type EditorTab = {
	id: string;
	name: string;
	sql: string;
	savedId: string | null;
	results: unknown[][];
	columns: string[];
	rowCount: number;
	durationMs: number;
	errorMsg: string;
	statusText: string;
	page: number;
};

function emptyTab(id: string, overrides: Partial<EditorTab> = {}): EditorTab {
	return {
		id,
		name: 'Untitled',
		sql: 'SELECT 1;',
		savedId: null,
		results: [],
		columns: [],
		rowCount: 0,
		durationMs: 0,
		errorMsg: '',
		statusText: 'Ready',
		page: 1,
		...overrides
	};
}

function createEditorStore() {
	const tabs = $state<EditorTab[]>([emptyTab('1')]);
	let activeId = $state('1');
	let counter = 1;

	const activeTab = $derived(tabs.find((tab) => tab.id === activeId) ?? tabs[0]);

	function activate(id: string) {
		if (tabs.some((tab) => tab.id === id)) activeId = id;
	}

	function addTab(overrides: Partial<EditorTab> = {}): EditorTab {
		const tab = emptyTab(String(++counter), overrides);
		tabs.push(tab);
		activeId = tab.id;
		return tab;
	}

	function closeTab(id: string) {
		if (tabs.length <= 1) return;
		const index = tabs.findIndex((tab) => tab.id === id);
		if (index < 0) return;
		tabs.splice(index, 1);
		if (activeId === id) activeId = tabs[Math.max(0, index - 1)].id;
	}

	/** Focuses the tab already linked to this saved query, or opens a new one. */
	function openSavedQuery(query: SavedQueryRef): EditorTab {
		const existing = tabs.find((tab) => tab.savedId === query.id);
		if (existing) {
			activeId = existing.id;
			return existing;
		}
		return addTab({ name: query.name, sql: query.sql, savedId: query.id });
	}

	function markSaved(id: string, query: SavedQueryRef) {
		const tab = tabs.find((item) => item.id === id);
		if (!tab) return;
		tab.savedId = query.id;
		tab.name = query.name;
	}

	function updateSql(id: string, sql: string) {
		const tab = tabs.find((item) => item.id === id);
		if (tab) tab.sql = sql;
	}

	return {
		get tabs() {
			return tabs;
		},
		get activeId() {
			return activeId;
		},
		get activeTab() {
			return activeTab;
		},
		activate,
		addTab,
		closeTab,
		openSavedQuery,
		markSaved,
		updateSql
	};
}

export const editor = createEditorStore();
