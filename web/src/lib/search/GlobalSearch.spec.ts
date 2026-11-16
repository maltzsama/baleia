import { fireEvent, render, screen } from '@testing-library/svelte';
import { HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../test/server';
import GlobalSearch from './GlobalSearch.svelte';
import type { WorkspaceSearchItem } from './client';

const nav = vi.hoisted(() => ({ goto: vi.fn() }));
vi.mock('$app/navigation', () => ({ goto: nav.goto }));

const WS = '00000000-0000-0000-0000-000000000001';

const savedQuery = {
	kind: 'saved_query' as const,
	id: 'query-1',
	title: 'Revenue by region',
	subtitle: 'Finance',
	score: 3,
	content: {
		kind: 'saved_query',
		id: 'query-1',
		slug: 'revenue',
		title: 'Revenue by region',
		space: { id: 'space-1', name: 'Finance' },
		folder_path: [],
		updated_at: '2026-01-01T00:00:00Z'
	}
};

const table = {
	kind: 'table' as const,
	id: 'table-1',
	title: 'orders',
	subtitle: 'sales',
	score: 2,
	metadata: {
		kind: 'table' as const,
		table_id: 'table-1',
		catalog_id: 'catalog-1',
		namespace: ['sales'],
		table: 'orders',
		score: 1
	}
};

function answer(items: WorkspaceSearchItem[], expandedWith: string[] = []) {
	server.use(
		http.get('/workspaces/{id}/search', ({ response }) =>
			response(200).json({ items, facets: [], expanded_with: expandedWith })
		)
	);
}

async function type(value: string) {
	await fireEvent.input(screen.getByRole('combobox'), { target: { value } });
}

function renderSearch() {
	return render(GlobalSearch, {
		props: { open: true, workspaceId: WS, workspaceName: 'Finance' }
	});
}

describe('global search', () => {
	beforeEach(() => {
		nav.goto.mockReset();
	});

	it('asks for at least two characters instead of querying', async () => {
		renderSearch();

		expect(await screen.findByText(/Type at least 2 characters/)).toBeTruthy();
		await type('r');

		expect(await screen.findByText(/Type at least 2 characters/)).toBeTruthy();
	});

	it('lists results with their kind and opens a saved query', async () => {
		answer([savedQuery]);
		renderSearch();

		await type('rev');

		expect(await screen.findByText('Revenue by region')).toBeTruthy();
		expect(screen.getByText('Saved query')).toBeTruthy();

		await fireEvent.click(screen.getByRole('option', { name: /Revenue by region/ }));

		expect(nav.goto).toHaveBeenCalledWith('/sql/q/query-1');
	});

	it('opens a table in the data explorer', async () => {
		answer([table]);
		renderSearch();

		await type('ord');
		await fireEvent.click(await screen.findByRole('option', { name: /orders/ }));

		expect(nav.goto).toHaveBeenCalledWith('/data?catalog=catalog-1&ns=sales&table=orders');
	});

	// #1027: the server also searches the glossary's synonyms, and the surface
	// says which words were used.
	it('shows the glossary synonyms the search expanded with', async () => {
		answer([savedQuery], ['sales', 'income']);
		renderSearch();

		await type('rev');

		expect(await screen.findByText('Also searched: sales, income')).toBeTruthy();
	});

	it('says when nothing matches', async () => {
		answer([]);
		renderSearch();

		await type('zzz');

		expect(await screen.findByText('No matches.')).toBeTruthy();
	});

	it('retries a failed search', async () => {
		let fail = true;
		server.use(
			http.get('/workspaces/{id}/search', () =>
				fail
					? HttpResponse.json(
							{ code: 'service_unavailable', message: 'search down' },
							{ status: 503 }
						)
					: HttpResponse.json(
							{ items: [savedQuery], facets: [], expanded_with: [] },
							{ status: 200 }
						)
			)
		);
		renderSearch();

		await type('rev');
		expect(await screen.findByText('Search failed.')).toBeTruthy();
		expect(screen.queryByText(/search down/)).toBeNull();

		fail = false;
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

		expect(await screen.findByText('Revenue by region')).toBeTruthy();
	});
});
