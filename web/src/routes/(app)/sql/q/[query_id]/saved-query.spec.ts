import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { editor } from '$lib/stores/editor.svelte';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../../../test/server';
import { makeSavedQuery, makeSpace, makeWorkspace } from '../../../../../test/factories';

vi.mock('$app/state', () => ({
	page: { url: new URL('http://localhost/sql/q/q1'), params: { query_id: 'q1' } }
}));
vi.mock('$lib/components/Workbench.svelte', async () => ({
	default: (await import('../../../../../test/Stub.svelte')).default
}));

import SavedQueryPage from './+page.svelte';

const WS = '00000000-0000-0000-0000-000000000001';

function useBackend(query: () => Promise<Response | null>) {
	server.use(
		http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
		http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json([makeSpace()])),
		http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/queries/{query_id}', async ({ response }) => {
			const result = await query();
			return result ?? response(200).json(makeSavedQuery({ id: 'q1', name: 'Weekly revenue' }));
		})
	);
}

// #470: opening a saved query is visibly pending, and a failure can retry.
describe('saved query route', () => {
	beforeEach(() => {
		editor.resetTabs();
		workspaceStore.select(makeWorkspace({ id: WS }));
	});

	it('says the query is opening, then opens it in its own tab', async () => {
		let release: () => void = () => {};
		const gate = new Promise<void>((resolve) => (release = resolve));
		useBackend(async () => {
			await gate;
			return null;
		});
		render(SavedQueryPage);

		expect(await screen.findByRole('status')).toBeTruthy();
		expect(screen.getByText('Opening the saved query…')).toBeTruthy();
		release();
		await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
		expect(editor.activeTab.name).toBe('Weekly revenue');
	});

	it('shows the error with a retry and a way back', async () => {
		let failing = true;
		useBackend(async () =>
			failing
				? new Response(JSON.stringify({ code: 'not_found', message: 'gone' }), {
						status: 404,
						headers: { 'content-type': 'application/json' }
					})
				: null
		);
		render(SavedQueryPage);

		expect(await screen.findByText('Failed to open the saved query')).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Back to the workbench' }).getAttribute('href')).toBe(
			'/sql'
		);
		failing = false;
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
		await waitFor(() => expect(editor.activeTab.name).toBe('Weekly revenue'));
		expect(screen.getByTestId('stub')).toBeTruthy();
	});
});
