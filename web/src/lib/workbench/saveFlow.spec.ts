import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../test/server';
import { makeSavedQuery, makeSpace } from '../../test/factories';
import { editor } from '$lib/stores/editor.svelte';
import type { Conflict } from './conflict';
import SaveConflictDialog from './SaveConflictDialog.svelte';
import SaveQueryDialog from './SaveQueryDialog.svelte';

const WS = '00000000-0000-0000-0000-000000000001';

function savedTab() {
	editor.resetTabs();
	return editor.openSavedQuery(
		makeSavedQuery({ id: 'q1', version: 1, sql: 'SELECT mine', space_id: 'sp1' })
	);
}

describe('save flow', () => {
	beforeEach(() => {
		editor.resetTabs();
	});

	it('PATCHes a saved query with expected_version', async () => {
		const tab = savedTab();
		const captured: { body: Record<string, unknown> | null } = { body: null };
		server.use(
			http.patch('/workspaces/{id}/queries/{query_id}', async ({ request, response }) => {
				captured.body = (await request.json()) as Record<string, unknown>;
				return response(200).json(makeSavedQuery({ id: 'q1', version: 2, sql: 'SELECT mine' }));
			})
		);
		const onsaved = vi.fn();

		render(SaveQueryDialog, {
			props: {
				open: true,
				tab,
				workspaceId: WS,
				spaces: [makeSpace({ id: 'sp1', my_role: 'manager' })],
				folders: [],
				onconflict: vi.fn(),
				onsaved
			}
		});

		await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
		await waitFor(() => expect(onsaved).toHaveBeenCalled());
		expect(captured.body?.expected_version).toBe(1);
	});

	it('opens the conflict dialog on a 409 version_conflict', async () => {
		const tab = savedTab();
		server.use(
			http.patch('/workspaces/{id}/queries/{query_id}', ({ response }) =>
				response(409).json({
					code: 'version_conflict',
					message: 'the content changed since you loaded it'
				})
			),
			http.get('/workspaces/{id}/queries/{query_id}', ({ response }) =>
				response(200).json(makeSavedQuery({ id: 'q1', version: 2, sql: 'SELECT theirs' }))
			)
		);
		const onconflict = vi.fn<(conflict: Conflict) => void>();

		render(SaveQueryDialog, {
			props: {
				open: true,
				tab,
				workspaceId: WS,
				spaces: [makeSpace({ id: 'sp1', my_role: 'manager' })],
				folders: [],
				onconflict,
				onsaved: vi.fn()
			}
		});

		await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
		await waitFor(() => expect(onconflict).toHaveBeenCalled());
		expect(onconflict.mock.calls[0][0].currentVersion).toBe(2);
		expect(onconflict.mock.calls[0][0].theirs.sql).toBe('SELECT theirs');
	});

	it('Overwrite re-sends the update with the current version', async () => {
		const tab = savedTab();
		const captured: { body: Record<string, unknown> | null } = { body: null };
		server.use(
			http.patch('/workspaces/{id}/queries/{query_id}', async ({ request, response }) => {
				captured.body = (await request.json()) as Record<string, unknown>;
				return response(200).json(makeSavedQuery({ id: 'q1', version: 3, sql: 'SELECT mine' }));
			})
		);
		const onsaved = vi.fn();

		render(SaveConflictDialog, {
			props: {
				open: true,
				tab,
				workspaceId: WS,
				conflict: {
					currentVersion: 2,
					theirs: makeSavedQuery({ id: 'q1', version: 2, sql: 'SELECT theirs' })
				},
				onsaved,
				oncopy: vi.fn()
			}
		});

		await fireEvent.click(screen.getByRole('button', { name: 'Overwrite' }));
		await waitFor(() => expect(onsaved).toHaveBeenCalled());
		expect(captured.body?.expected_version).toBe(2);
	});
});
