import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('$lib/workbench/drafts', () => ({
	loadDrafts: vi.fn(),
	saveDrafts: vi.fn(),
	MAX_DRAFT_TABS: 30,
	MAX_DRAFT_SQL: 1 << 20
}));

import { loadDrafts, saveDrafts } from '$lib/workbench/drafts';
import { editor } from './editor.svelte';

const query = {
	id: 'q1',
	name: 'Revenue',
	sql: 'SELECT 1',
	workspace_id: 'ws1',
	space_id: 'sp1',
	slug: 'revenue',
	version: 1,
	folder_id: null,
	created_by: 'u1',
	created_at: '2026-01-01T00:00:00Z',
	updated_at: '2026-01-01T00:00:00Z',
	target: undefined
};

describe('editor store', () => {
	beforeEach(() => {
		editor.resetTabs();
		vi.mocked(loadDrafts).mockReset();
		vi.mocked(saveDrafts).mockReset();
	});

	it('opens a saved query in a new tab and focuses it', () => {
		const tab = editor.openSavedQuery(query);
		expect(tab.savedId).toBe('q1');
		expect(tab.name).toBe('Revenue');
		expect(editor.activeId).toBe(tab.id);
	});

	it('focuses the existing tab when the same query is opened twice', () => {
		const first = editor.openSavedQuery(query);
		const tabsBefore = editor.tabs.length;
		const second = editor.openSavedQuery(query);
		expect(second.id).toBe(first.id);
		expect(editor.tabs.length).toBe(tabsBefore);
	});

	it('records the version and opens read-only for a viewer', () => {
		const tab = editor.openSavedQuery(query, true);
		expect(tab.readOnly).toBe(true);
		expect(tab.savedRef).toEqual({
			id: 'q1',
			version: 1,
			slug: 'revenue',
			space_id: 'sp1',
			folder_id: null
		});
	});

	it('never overwrites a dirty open tab', () => {
		const tab = editor.openSavedQuery(query);
		editor.updateSql(tab.id, 'SELECT 2');
		expect(tab.dirty).toBe(true);
		editor.openSavedQuery({ ...query, sql: 'SELECT 3' });
		expect(editor.activeTab.sql).toBe('SELECT 2');
	});

	it('clears dirty and advances the version after a save', () => {
		const tab = editor.openSavedQuery(query);
		editor.updateSql(tab.id, 'SELECT 2');
		editor.markSaved(tab.id, { ...query, version: 2, sql: 'SELECT 2' });
		expect(tab.dirty).toBe(false);
		expect(tab.savedRef?.version).toBe(2);
		expect(tab.savedSql).toBe('SELECT 2');
	});

	it('hydrates the drafts of a user and workspace', async () => {
		vi.mocked(loadDrafts).mockResolvedValueOnce([
			{
				id: '7',
				name: 'Draft',
				sql: 'SELECT 9',
				savedRef: null,
				bindingId: 'b1',
				schema: 's',
				readOnly: false,
				savedSql: 'SELECT 9',
				savedBindingId: '',
				savedSchema: ''
			}
		]);
		await editor.hydrate('u1', 'w1');
		expect(editor.tabs[0].name).toBe('Draft');
		expect(editor.tabs[0].sql).toBe('SELECT 9');
	});

	it('persists through the drafts module', async () => {
		await editor.persist('u1', 'w1');
		expect(saveDrafts).toHaveBeenCalledWith('u1', 'w1', editor.tabs);
	});
});
