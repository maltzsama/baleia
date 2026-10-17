import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { EditorTab } from '$lib/stores/editor.svelte';

vi.mock('idb-keyval', () => ({
	get: vi.fn(),
	set: vi.fn(),
	del: vi.fn(),
	keys: vi.fn()
}));

import { del, get, keys, set } from 'idb-keyval';
import { clearDrafts, loadDrafts, MAX_DRAFT_SQL, MAX_DRAFT_TABS, saveDrafts } from './drafts';

function tab(id: string, sql = 'SELECT 1'): EditorTab {
	return {
		id,
		name: `Tab ${id}`,
		sql,
		savedId: null,
		savedRef: null,
		bindingId: 'b1',
		schema: 's',
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
		savedSchema: ''
	};
}

describe('workbench drafts', () => {
	beforeEach(() => {
		vi.mocked(get).mockReset();
		vi.mocked(set).mockReset();
		vi.mocked(keys).mockReset();
		vi.mocked(del).mockReset();
	});

	it('persists at most 30 tabs and skips SQL over 1 MiB', async () => {
		const tabs = Array.from({ length: MAX_DRAFT_TABS + 5 }, (_, i) => tab(String(i)));
		tabs.push(tab('huge', 'x'.repeat(MAX_DRAFT_SQL + 1)));

		await saveDrafts('u1', 'w1', tabs);

		const [, stored] = vi.mocked(set).mock.calls[0];
		expect((stored as EditorTab[]).length).toBe(MAX_DRAFT_TABS);
		expect((stored as EditorTab[]).some((item) => item.sql.length > MAX_DRAFT_SQL)).toBe(false);
	});

	it('reads drafts and falls back to memory when IndexedDB is unavailable', async () => {
		vi.mocked(get).mockResolvedValueOnce([tab('a')]);
		expect(await loadDrafts('u1', 'w1')).toHaveLength(1);

		vi.mocked(get).mockRejectedValueOnce(new Error('no indexeddb'));
		expect(await loadDrafts('u1', 'w1')).toEqual([]);
	});

	it('never throws when writing fails', async () => {
		vi.mocked(set).mockRejectedValueOnce(new Error('quota'));
		await expect(saveDrafts('u1', 'w1', [tab('a')])).resolves.toBeUndefined();
	});

	it('clears only the user keys on explicit logout', async () => {
		vi.mocked(keys).mockResolvedValueOnce([
			'baleia.tabs.u1.w1',
			'baleia.tabs.u1.w2',
			'baleia.tabs.u2.w1'
		]);
		vi.mocked(del).mockResolvedValue(undefined);

		await clearDrafts('u1');

		expect(vi.mocked(del).mock.calls.map(([key]) => key)).toEqual([
			'baleia.tabs.u1.w1',
			'baleia.tabs.u1.w2'
		]);
	});
});
