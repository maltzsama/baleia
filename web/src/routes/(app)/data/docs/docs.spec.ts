import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../../test/server';
import { makeWorkspace } from '../../../../test/factories';

const goto = vi.hoisted(() => vi.fn());
vi.mock('$app/navigation', () => ({ goto }));

import DocsPage from './+page.svelte';

const WS = '00000000-0000-0000-0000-000000000001';
const CATALOG = '00000000-0000-0000-0000-0000000000a2';

function doc(column: string | null) {
	return {
		id: column ? 'd-col' : 'd-table',
		catalog_id: CATALOG,
		namespace: ['sales'],
		table: 'orders',
		column,
		description: 'Revenue notes',
		tags: [],
		updated_by: 'u',
		updated_at: '2026-01-01T00:00:00Z',
		score: 1
	};
}

// #474: a column result opens the explorer on that column; a table result
// opens the table only.
describe('data docs', () => {
	beforeEach(() => {
		goto.mockReset();
		workspaceStore.select(makeWorkspace({ id: WS }));
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
			http.get('/workspaces/{id}/semantic/tags', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/principals', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/semantic/docs/search', ({ response }) =>
				response(200).json([doc('order note'), doc(null)])
			)
		);
	});

	it.each([
		[0, 'order note'],
		[1, null]
	])('opens result %i with column %s', async (index, column) => {
		render(DocsPage);
		await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'revenue' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Search' }));
		await waitFor(() =>
			expect(screen.getAllByRole('button', { name: /sales\.orders/ })).toHaveLength(2)
		);

		await fireEvent.click(screen.getAllByRole('button', { name: /sales\.orders/ })[index]);
		expect(goto).toHaveBeenCalledOnce();
		const url = new URL(String(goto.mock.calls[0][0]), 'http://localhost');
		expect(url.pathname).toBe('/data');
		expect(url.searchParams.get('catalog')).toBe(CATALOG);
		expect(url.searchParams.get('table')).toBe('orders');
		expect(url.searchParams.get('column')).toBe(column);
	});
});
