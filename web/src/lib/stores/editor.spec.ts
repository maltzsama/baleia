import { beforeEach, describe, expect, it } from 'vitest';
import { editor } from './editor.svelte';

const query = {
	id: 'q1',
	name: 'Revenue',
	sql: 'SELECT 1',
	workspace_id: 'ws1',
	folder_id: null,
	created_by: 'u1',
	created_at: '2026-01-01T00:00:00Z',
	updated_at: '2026-01-01T00:00:00Z',
	allowed_actions: []
};

describe('editor store', () => {
	beforeEach(() => {
		// Return to the initial tab so tests stay independent.
		for (const tab of [...editor.tabs].slice(1)) editor.closeTab(tab.id);
		if (editor.tabs[0]) editor.activate(editor.tabs[0].id);
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
});
