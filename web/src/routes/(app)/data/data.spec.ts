import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { clearMetadataCache } from '$lib/metadata/client';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../test/server';
import { setTestUrl } from '../../../test/nav.svelte';
import {
	makeMetadataCatalog,
	makeSearchResult,
	makeTableDetail,
	makeWorkspace
} from '../../../test/factories';

const goto = vi.hoisted(() => vi.fn());
vi.mock('$app/state', async () => ({ page: (await import('../../../test/nav.svelte')).testPage }));
vi.mock('$app/navigation', () => ({ goto }));

import DataPage from './+page.svelte';

const WS = '00000000-0000-0000-0000-000000000001';
const LAKE = '00000000-0000-0000-0000-0000000000a1';
const WAREHOUSE = '00000000-0000-0000-0000-0000000000a2';
const LAKE_ORDERS = '00000000-0000-0000-0000-0000000000d1';
const WAREHOUSE_ORDERS = '00000000-0000-0000-0000-0000000000d2';
const tableIn: Record<string, string> = { [LAKE]: LAKE_ORDERS, [WAREHOUSE]: WAREHOUSE_ORDERS };

// Two granted catalogs that both hold sales.orders (#455).
function useBackend(options: { searchFails?: boolean } = {}) {
	const opened: string[] = [];
	server.use(
		http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
		http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
			response(200).json([
				makeMetadataCatalog({ id: LAKE, name: 'lake' }),
				makeMetadataCatalog({ id: WAREHOUSE, name: 'warehouse' })
			])
		),
		http.get(
			'/workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces',
			({ request, response }) => {
				const parent = new URL(request.url).searchParams.getAll('parent');
				return response(200).json({
					freshness: { metadata_synced_at: null },
					items: parent.length === 0 ? [{ path: ['sales'] }] : []
				});
			}
		),
		http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/tables', ({ params, response }) =>
			response(200).json({
				freshness: { metadata_synced_at: null },
				items: [{ id: tableIn[params.catalog_id], name: 'orders', kind: 'table' }],
				next_cursor: null
			})
		),
		http.get('/workspaces/{id}/metadata/search', ({ request, response }) => {
			if (options.searchFails) {
				return response(404).json({ code: 'not_found', message: 'workspace not found' });
			}
			const catalog = new URL(request.url).searchParams.get('catalog_id');
			// Best first and unscoped by default: the lake hit comes first.
			const all = [
				makeSearchResult({ catalog_id: LAKE, table_id: LAKE_ORDERS }),
				makeSearchResult({ catalog_id: WAREHOUSE, table_id: WAREHOUSE_ORDERS })
			];
			return response(200).json(all.filter((hit) => !catalog || hit.catalog_id === catalog));
		}),
		http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ params, response }) => {
			opened.push(params.table_id);
			const catalog = params.table_id === LAKE_ORDERS ? LAKE : WAREHOUSE;
			return response(200).json(makeTableDetail({ id: params.table_id, catalog_id: catalog }));
		})
	);
	return opened;
}

function linkTo(catalog: string) {
	setTestUrl(`/data?catalog=${catalog}&ns=sales&table=orders`);
}

describe('data page deep link', () => {
	beforeEach(() => {
		clearMetadataCache();
		goto.mockReset();
		workspaceStore.select(makeWorkspace({ id: WS }));
	});

	it('opens the table of the linked catalog, not the first match', async () => {
		const opened = useBackend();
		linkTo(WAREHOUSE);
		render(DataPage);

		await waitFor(() => expect(opened).toEqual([WAREHOUSE_ORDERS]));
		// The tree reveals the linked row under its catalog.
		const row = await screen.findByRole('button', { name: /orders/, current: true });
		expect(row).toBeTruthy();
	});

	it('still opens a link that carries the catalog name', async () => {
		const opened = useBackend();
		linkTo('warehouse');
		render(DataPage);

		await waitFor(() => expect(opened).toEqual([WAREHOUSE_ORDERS]));
	});

	it('says the table was not found instead of opening another one', async () => {
		const opened = useBackend();
		setTestUrl(`/data?catalog=${LAKE}&ns=sales&table=missing`);
		render(DataPage);

		expect(await screen.findByText('Table not found')).toBeTruthy();
		expect(opened).toEqual([]);
	});

	it('shows an error with a retry when the lookup fails', async () => {
		useBackend({ searchFails: true });
		linkTo(LAKE);
		render(DataPage);

		expect(await screen.findByText('Could not open the linked table')).toBeTruthy();
		expect(screen.getByRole('button', { name: /try again/i })).toBeTruthy();
	});

	it('writes the catalog id when a table is picked in the tree', async () => {
		useBackend();
		setTestUrl('/data');
		render(DataPage);

		await fireEvent.click(await screen.findByRole('button', { name: /warehouse/i }));
		await fireEvent.click(await screen.findByRole('button', { name: /sales/i }));
		await fireEvent.click(await screen.findByRole('button', { name: /orders/i }));

		await waitFor(() => expect(goto).toHaveBeenCalled());
		const url = new URL(String(goto.mock.calls.at(-1)?.[0]), 'http://localhost');
		expect(url.searchParams.get('catalog')).toBe(WAREHOUSE);
		expect(url.searchParams.get('table')).toBe('orders');
	});
	// #474: a link that names a column opens the table on that column.
	it('expands and focuses the linked column', async () => {
		useBackend();
		setTestUrl(`/data?catalog=${WAREHOUSE}&ns=sales&table=orders&column=order_note`);
		render(DataPage);

		const row = await screen.findByText('order_note');
		await waitFor(() =>
			expect(row.closest('[data-column]')?.textContent).toContain('No description yet.')
		);
		await waitFor(() =>
			expect(document.activeElement?.closest('[data-column]')).toBe(row.closest('[data-column]'))
		);
	});

	it('drops the column when another table is picked', async () => {
		useBackend();
		setTestUrl(`/data?catalog=${WAREHOUSE}&ns=sales&table=orders&column=order_note`);
		render(DataPage);
		await screen.findByText('order_note');
		goto.mockReset();

		await fireEvent.click(await screen.findByRole('button', { name: /lake/i }));
		// lake (listed first) now shows its own sales namespace above warehouse's.
		await waitFor(() => expect(screen.getAllByRole('button', { name: /^sales$/ })).toHaveLength(2));
		await fireEvent.click(screen.getAllByRole('button', { name: /^sales$/ })[0]);
		await waitFor(() =>
			expect(screen.getAllByRole('button', { name: /^orders$/ })).toHaveLength(2)
		);
		await fireEvent.click(screen.getAllByRole('button', { name: /^orders$/ })[0]);
		await waitFor(() => expect(goto).toHaveBeenCalled());
		const url = new URL(String(goto.mock.calls.at(-1)?.[0]), 'http://localhost');
		expect(url.searchParams.get('catalog')).toBe(LAKE);
		expect(url.searchParams.get('column')).toBeNull();
	});
});
