// Typed workbench state (#29, #112). Components read and mutate tabs here
// instead of exchanging `window` CustomEvents. Drafts persist per user ×
// workspace through IndexedDB (#112).
import type { components } from '$lib/api/schema';
import { loadDrafts, saveDrafts, type PersistedTab } from '$lib/workbench/drafts';

export type SavedQuery = components['schemas']['SavedQuery'];
export type ExecutionTarget = components['schemas']['ExecutionTarget'];
export type SavedRef = {
	id: string;
	version: number;
	slug: string;
	space_id: string;
	folder_id: string | null;
};

export type EditorTab = {
	id: string;
	name: string;
	sql: string;
	savedId: string | null;
	/** The version and location the tab was saved at (#112). */
	savedRef: SavedRef | null;
	/** Where the tab runs (#24): a target binding and an optional schema. */
	bindingId: string;
	schema: string;
	executionId: string;
	/** The target the engine resolved for the last run, as reported by the API. */
	runTarget: ExecutionTarget | null;
	results: unknown[][];
	columns: string[];
	rowCount: number;
	durationMs: number;
	errorMsg: string;
	statusText: string;
	page: number;
	/** A viewer opens saved content read-only (#112). */
	readOnly: boolean;
	/** The SQL or target differs from the saved snapshot. */
	dirty: boolean;
	savedSql: string;
	savedBindingId: string;
	savedSchema: string;
};

function emptyTab(id: string, overrides: Partial<EditorTab> = {}): EditorTab {
	const sql = overrides.sql ?? '';
	return {
		id,
		name: 'Untitled',
		sql,
		savedId: null,
		savedRef: null,
		bindingId: '',
		schema: '',
		executionId: '',
		runTarget: null,
		results: [],
		columns: [],
		rowCount: 0,
		durationMs: 0,
		errorMsg: '',
		statusText: 'Ready',
		page: 1,
		readOnly: false,
		dirty: false,
		savedSql: sql,
		savedBindingId: '',
		savedSchema: '',
		...overrides
	};
}

function fromPersisted(tab: PersistedTab): EditorTab {
	return emptyTab(tab.id, {
		name: tab.name,
		sql: tab.sql,
		savedRef: tab.savedRef,
		savedId: tab.savedRef?.id ?? null,
		bindingId: tab.bindingId,
		schema: tab.schema,
		readOnly: tab.readOnly,
		savedSql: tab.savedSql,
		savedBindingId: tab.savedBindingId,
		savedSchema: tab.savedSchema
	});
}

export type EditorInsert = { tabId: string; text: string; seq: number };
export type EditorRun = { tabId: string; seq: number };

function createEditorStore() {
	const tabs = $state<EditorTab[]>([emptyTab('1')]);
	let activeId = $state('1');
	let counter = 1;
	// A pending insert is a typed store channel, never a window event: the
	// editor applies it at the cursor and bumps `seq` (FD rule).
	let insertSeq = 0;
	let pendingInsert = $state<EditorInsert | null>(null);
	// "Re-run" from history (#36) opens a tab and asks the editor to run it.
	let runSeq = 0;
	let pendingRun = $state<EditorRun | null>(null);
	// Bumped whenever the tabs are replaced wholesale (drafts, reset), so the
	// editor re-mirrors the active tab even when its id did not change.
	let generation = $state(0);

	const activeTab = $derived(tabs.find((tab) => tab.id === activeId) ?? tabs[0]);
	const dirty = $derived(tabs.some((tab) => tab.dirty));

	function activate(id: string) {
		if (tabs.some((tab) => tab.id === id)) activeId = id;
	}

	function addTab(overrides: Partial<EditorTab> = {}): EditorTab {
		tabs.push(emptyTab(String(++counter), overrides));
		// Return the reactive proxy, not the raw object pushed above.
		const tab = tabs[tabs.length - 1];
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
	function openSavedQuery(query: SavedQuery, readOnly = false): EditorTab {
		const existing = tabs.find((tab) => tab.savedId === query.id);
		if (existing) {
			// Never overwrite local edits of an already-open dirty tab.
			activeId = existing.id;
			return existing;
		}
		return addTab({
			name: query.name,
			sql: query.sql,
			savedId: query.id,
			savedRef: {
				id: query.id,
				version: query.version,
				slug: query.slug,
				space_id: query.space_id,
				folder_id: query.folder_id ?? null
			},
			bindingId: query.target?.binding_id ?? '',
			schema: query.target?.schema ?? '',
			readOnly,
			savedSql: query.sql,
			savedBindingId: query.target?.binding_id ?? '',
			savedSchema: query.target?.schema ?? ''
		});
	}

	function markSaved(id: string, query: SavedQuery) {
		const tab = tabs.find((item) => item.id === id);
		if (!tab) return;
		tab.savedId = query.id;
		tab.savedRef = {
			id: query.id,
			version: query.version,
			slug: query.slug,
			space_id: query.space_id,
			folder_id: query.folder_id ?? null
		};
		tab.name = query.name;
		tab.savedSql = tab.sql;
		tab.savedBindingId = tab.bindingId;
		tab.savedSchema = tab.schema;
		tab.dirty = false;
		tab.readOnly = false;
	}

	function updateSql(id: string, sql: string) {
		const tab = tabs.find((item) => item.id === id);
		if (!tab) return;
		tab.sql = sql;
		tab.dirty = sql !== tab.savedSql;
	}

	/** Asks the active tab's editor to insert text at the cursor. */
	function insert(text: string) {
		if (!text) return;
		pendingInsert = { tabId: activeId, text, seq: ++insertSeq };
	}

	/**
	 * Records the target (binding + schema) the tab runs on. A user choice
	 * marks the tab dirty; the automatic default assignment does not.
	 */
	function setTarget(id: string, bindingId: string, schema: string, userChanged = false) {
		const tab = tabs.find((item) => item.id === id);
		if (!tab) return;
		tab.bindingId = bindingId;
		tab.schema = schema;
		if (userChanged) tab.dirty = true;
	}

	function setExecution(id: string, executionId: string) {
		const tab = tabs.find((item) => item.id === id);
		if (tab) tab.executionId = executionId;
	}

	/**
	 * Opens the workbench on a new tab with this SQL and target and asks the
	 * editor to run it (#36 "Re-run").
	 */
	function openExecution(query: {
		name?: string;
		sql: string;
		bindingId: string;
		schema: string;
	}): EditorTab {
		const tab = addTab({
			name: query.name ?? 'History',
			sql: query.sql,
			bindingId: query.bindingId,
			schema: query.schema
		});
		pendingRun = { tabId: tab.id, seq: ++runSeq };
		return tab;
	}

	/** Replaces the tabs with the user's drafts for a workspace. */
	async function hydrate(userId: string, workspaceId: string) {
		const drafts = await loadDrafts(userId, workspaceId);
		if (drafts.length === 0) return;
		tabs.splice(0, tabs.length, ...drafts.map(fromPersisted));
		activeId = tabs[0].id;
		counter = Math.max(counter, ...tabs.map((tab) => Number(tab.id) || 0));
		generation++;
	}

	/** Persists the current tabs for a user and workspace. Never throws. */
	function persist(userId: string, workspaceId: string): Promise<void> {
		return saveDrafts(userId, workspaceId, tabs);
	}

	function resetTabs() {
		tabs.splice(0, tabs.length, emptyTab('1'));
		activeId = '1';
		counter = 1;
		generation++;
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
		get pendingInsert() {
			return pendingInsert;
		},
		get pendingRun() {
			return pendingRun;
		},
		get dirty() {
			return dirty;
		},
		get generation() {
			return generation;
		},
		activate,
		addTab,
		closeTab,
		openSavedQuery,
		markSaved,
		updateSql,
		insert,
		setTarget,
		setExecution,
		openExecution,
		hydrate,
		persist,
		resetTabs
	};
}

export const editor = createEditorStore();
