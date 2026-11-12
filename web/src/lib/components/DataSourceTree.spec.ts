import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { catalogAccessStore } from '$lib/stores/catalogAccess.svelte';
import { clearMetadataCache } from '$lib/metadata/client';
import { http, server } from '../../test/server';
import {
	makeMetadataBinding,
	makeMetadataCatalog,
	makeSearchItem,
	makeSearchResponse,
	makeSearchResult,
	makeWorkspace
} from '../../test/factories';
import DataSourceTree from './DataSourceTree.svelte';
import LeftSidebar from './LeftSidebar.svelte';

const TABLE_ID = '00000000-0000-0000-0000-0000000000d1';

function mockTree() {
	let namespaceCalls = 0;
	let tableCalls = 0;
	server.use(
		http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
			response(200).json([makeMetadataCatalog()])
		),
		http.get(
			'/workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces',
			({ request, response }) => {
				namespaceCalls += 1;
				const parent = new URL(request.url).searchParams.getAll('parent');
				return response(200).json({
					freshness: { metadata_synced_at: null },
					items: parent.length === 0 ? [{ path: ['sales'] }] : []
				});
			}
		),
		http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/tables', ({ response }) => {
			tableCalls += 1;
			return response(200).json({
				freshness: { metadata_synced_at: null },
				items: [{ id: TABLE_ID, name: 'orders', kind: 'table' }],
				next_cursor: null
			});
		})
	);
	return {
		get namespaceCalls() {
			return namespaceCalls;
		},
		get tableCalls() {
			return tableCalls;
		}
	};
}

describe('DataSourceTree', () => {
	beforeEach(() => {
		clearMetadataCache();
		workspaceStore.select(makeWorkspace());
	});

	it('expands from the metadata API and caches a revisited node', async () => {
		const calls = mockTree();
		const onSelectTable = vi.fn();
		render(DataSourceTree, { props: { onSelectTable } });

		await fireEvent.click(await screen.findByRole('button', { name: /lake/i }));
		expect(await screen.findByText('sales')).toBeTruthy();
		expect(calls.namespaceCalls).toBe(1);

		await fireEvent.click(screen.getByRole('button', { name: /sales/i }));
		expect(await screen.findByText('orders')).toBeTruthy();
		expect(calls.tableCalls).toBe(1);

		// Collapse and expand again: the cached read is reused.
		await fireEvent.click(screen.getByRole('button', { name: /sales/i }));
		await fireEvent.click(screen.getByRole('button', { name: /sales/i }));
		expect(await screen.findByText('orders')).toBeTruthy();
		expect(calls.namespaceCalls).toBe(2); // root + sales, not a third
		expect(calls.tableCalls).toBe(1);

		await fireEvent.click(screen.getByText('orders'));
		expect(onSelectTable).toHaveBeenCalledWith(
			expect.objectContaining({
				tableId: TABLE_ID,
				catalogName: 'lake',
				namespace: ['sales'],
				tableName: 'orders'
			})
		);
	});

	it('shows a catalog granted to the selected workspace without a reload (#975)', async () => {
		let granted = false;
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json(
					granted
						? [
								makeMetadataCatalog(),
								makeMetadataCatalog({
									id: '00000000-0000-0000-0000-0000000000c2',
									name: 'Granted Lake'
								})
							]
						: [makeMetadataCatalog()]
				)
			)
		);
		render(DataSourceTree, { props: { onSelectTable: vi.fn() } });
		await screen.findByText('lake');
		expect(screen.queryByText('Granted Lake')).toBeNull();

		// The grant lands on the server, then the grant flow signals it.
		granted = true;
		catalogAccessStore.changed();
		expect(await screen.findByText('Granted Lake')).toBeTruthy();
	});

	it('shows a table the source cannot read disabled, with why (#977)', async () => {
		const onSelectTable = vi.fn();
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog()])
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
			http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/tables', ({ response }) =>
				response(200).json({
					freshness: { metadata_synced_at: null },
					items: [
						{ id: TABLE_ID, name: 'orders', kind: 'table' },
						{
							id: '00000000-0000-0000-0000-0000000000d2',
							name: 'locked',
							kind: 'table',
							unsupported: { code: 'table_kind', detail: 'TABLE_DB_STORAGE' }
						}
					],
					next_cursor: null
				})
			)
		);
		render(DataSourceTree, { props: { onSelectTable } });
		await fireEvent.click(await screen.findByRole('button', { name: /lake/i }));
		await fireEvent.click(await screen.findByRole('button', { name: /sales/i }));

		// The readable table is a button; the unsupported one is not, but explains itself.
		expect(await screen.findByRole('button', { name: /orders/i })).toBeTruthy();
		expect(screen.queryByRole('button', { name: /^locked/i })).toBeNull();
		const why = screen.getByRole('button', { name: /Unsupported table: type: TABLE_DB_STORAGE/ });
		expect(why.getAttribute('title')).toContain('type: TABLE_DB_STORAGE');
		expect(why.getAttribute('title')).toContain('Unsupported type');
		expect(document.querySelector('[data-unsupported-table]')?.textContent).toContain('locked');

		// Clicking the disabled row selects nothing.
		await fireEvent.click(screen.getByText('locked'));
		expect(onSelectTable).not.toHaveBeenCalled();

		// The reason is also reachable without a pointer: the info button reveals it.
		expect(why.getAttribute('aria-expanded')).toBe('false');
		await fireEvent.click(why);
		expect(why.getAttribute('aria-expanded')).toBe('true');
		expect((await screen.findByRole('note')).textContent).toContain('type: TABLE_DB_STORAGE');
		await fireEvent.click(why);
		expect(screen.queryByRole('note')).toBeNull();
	});

	it('clears a root load error once a refresh works (#975)', async () => {
		let failing = true;
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				failing
					? response(404).json({ code: 'not_found', message: 'boom' })
					: response(200).json([makeMetadataCatalog()])
			)
		);
		render(DataSourceTree, { props: { onSelectTable: vi.fn() } });
		expect(await screen.findByText('Could not load catalogs')).toBeTruthy();

		failing = false;
		catalogAccessStore.changed();
		expect(await screen.findByText('lake')).toBeTruthy();
		expect(screen.queryByText('Could not load catalogs')).toBeNull();
	});

	it('shows the name a query uses beside a catalog whose display name differs', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog({ name: 'Polaris Lake' })])
			)
		);
		render(DataSourceTree, { props: { onSelectTable: vi.fn() } });

		await screen.findByText('Polaris Lake');
		expect(screen.getByText('lake_t')).toBeTruthy();
	});

	it('shows each engine its own query name, and tells them apart in the catalog view', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([
					makeMetadataCatalog({
						name: 'Shared Lake',
						bindings: [
							makeMetadataBinding({
								binding_id: '00000000-0000-0000-0000-0000000000b1',
								engine_id: '00000000-0000-0000-0000-0000000000e1',
								engine_name: 'trino-a',
								engine_catalog_name: 'lake_a'
							}),
							makeMetadataBinding({
								binding_id: '00000000-0000-0000-0000-0000000000b2',
								engine_id: '00000000-0000-0000-0000-0000000000e2',
								engine_name: 'trino-b',
								engine_catalog_name: 'lake_b'
							})
						]
					})
				])
			)
		);
		render(DataSourceTree, { props: { onSelectTable: vi.fn() } });

		// Catalog view: each name carries the engine it belongs to.
		expect(await screen.findByText('lake_a (trino-a), lake_b (trino-b)')).toBeTruthy();

		// Engine view: under each engine only that engine's name.
		await fireEvent.click(screen.getByRole('button', { name: 'By engine' }));
		expect(await screen.findAllByText('lake_a')).toHaveLength(1);
		expect(screen.getAllByText('lake_b')).toHaveLength(1);
		expect(screen.queryByText(/lake_a \(/)).toBeNull();
	});

	it('renders a catalog whose query name is an Object property name', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([
					makeMetadataCatalog({
						name: 'Odd',
						bindings: [makeMetadataBinding({ engine_catalog_name: 'constructor' })]
					})
				])
			)
		);
		render(DataSourceTree, { props: { onSelectTable: vi.fn() } });

		expect(await screen.findByText('constructor')).toBeTruthy();
	});

	it('renders namespaces before tables finish loading', async () => {
		let resolveTables!: () => void;
		const tablesGate = new Promise<void>((resolve) => (resolveTables = resolve));
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog()])
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
			http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/tables', async ({ response }) => {
				await tablesGate;
				return response(200).json({
					freshness: { metadata_synced_at: null },
					items: [{ id: TABLE_ID, name: 'orders', kind: 'table' }],
					next_cursor: null
				});
			})
		);

		render(DataSourceTree, { props: {} });

		await fireEvent.click(await screen.findByRole('button', { name: /lake/i }));
		expect(await screen.findByText('sales')).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: /sales/i }));
		// The namespaces already resolved, so the leaf shows its tables-loading
		// state rather than hiding behind the pending tables request.
		expect(await screen.findByText(/Loading tables/)).toBeTruthy();

		resolveTables();
		expect(await screen.findByText('orders')).toBeTruthy();
	});

	it('shows an error state with retry when a level fails', async () => {
		let fail = true;
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog()])
			),
			http.get(
				'/workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces',
				({ request, response }) => {
					if (fail) return response(404).json({ code: 'not_found', message: 'snapshot offline' });
					const parent = new URL(request.url).searchParams.getAll('parent');
					return response(200).json({
						freshness: { metadata_synced_at: null },
						items: parent.length === 0 ? [{ path: ['sales'] }] : []
					});
				}
			)
		);
		render(DataSourceTree, { props: {} });

		await fireEvent.click(await screen.findByRole('button', { name: /lake/i }));
		expect(await screen.findByText('Could not load this level')).toBeTruthy();

		fail = false;
		await fireEvent.click(screen.getByRole('button', { name: /try again/i }));
		expect(await screen.findByText('sales')).toBeTruthy();
	});

	it('debounces search and issues one workspace search request (#943)', async () => {
		let searchCalls = 0;
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog()])
			),
			http.get('/workspaces/{id}/search', ({ request, response }) => {
				searchCalls += 1;
				// The sidebar only wants tables and columns.
				expect(new URL(request.url).searchParams.getAll('kind').sort()).toEqual([
					'column',
					'table'
				]);
				return response(200).json(
					makeSearchResponse([
						makeSearchItem(),
						makeSearchItem({
							kind: 'column',
							id: 'order_note',
							title: 'order_note',
							metadata: makeSearchResult({
								kind: 'column',
								column: 'order_note',
								native_type: 'varchar'
							})
						})
					])
				);
			}),
			// The reveal path expands the tree to the hit.
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
			http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/tables', ({ response }) =>
				response(200).json({
					freshness: { metadata_synced_at: null },
					items: [{ id: TABLE_ID, name: 'orders', kind: 'table' }],
					next_cursor: null
				})
			)
		);

		const onSelectTable = vi.fn();
		render(LeftSidebar, { props: { onSelectTable } });
		const input = screen.getByLabelText('Search tables and columns');

		// Rapid typing within the 250 ms window still issues one request.
		await fireEvent.input(input, { target: { value: 'o' } });
		await fireEvent.input(input, { target: { value: 'or' } });
		await fireEvent.input(input, { target: { value: 'ord' } });

		await waitFor(() => expect(searchCalls).toBe(1));

		expect(await screen.findByText('Tables')).toBeTruthy();
		expect(await screen.findByText('Columns')).toBeTruthy();
		expect(screen.getAllByRole('option')).toHaveLength(2);

		// #472: the input is a combobox over the results and names the active one.
		expect(input.getAttribute('role')).toBe('combobox');
		expect(input.getAttribute('aria-expanded')).toBe('true');
		expect(input.getAttribute('aria-controls')).toBe(screen.getByRole('listbox').id);
		const active = () => document.getElementById(input.getAttribute('aria-activedescendant')!);
		expect(active()?.getAttribute('aria-selected')).toBe('true');
		await fireEvent.keyDown(input, { key: 'ArrowDown' });
		expect(active()?.textContent).toContain('orders.');
		await fireEvent.keyDown(input, { key: 'ArrowUp' });

		// Escape clears the search and goes back to the tree.
		await fireEvent.keyDown(input, { key: 'Escape' });
		expect((input as HTMLInputElement).value).toBe('');
		expect(input.getAttribute('aria-expanded')).toBe('false');
		await fireEvent.input(input, { target: { value: 'ord' } });
		await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2));

		// Enter selects the first hit and reveals it in the tree.
		await fireEvent.keyDown(input, { key: 'Enter' });
		await waitFor(() =>
			expect(onSelectTable).toHaveBeenCalledWith(
				expect.objectContaining({ tableId: TABLE_ID, namespace: ['sales'] })
			)
		);
	});
	it('keeps the metadata search for the tag, certified and namespace filters', async () => {
		let metadataCalls = 0;
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog()])
			),
			http.get('/workspaces/{id}/metadata/search', ({ request, response }) => {
				metadataCalls += 1;
				expect(new URL(request.url).searchParams.get('certified')).toBe('true');
				return response(200).json([makeSearchResult()]);
			})
		);
		render(LeftSidebar, { props: { onSelectTable: vi.fn() } });
		await fireEvent.input(screen.getByLabelText('Search tables and columns'), {
			target: { value: 'orders certified:true' }
		});
		await waitFor(() => expect(metadataCalls).toBe(1));
		expect(await screen.findByText('Tables')).toBeTruthy();
	});

	it('says which glossary synonyms the search also looked for (#943)', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog()])
			),
			http.get('/workspaces/{id}/search', ({ response }) =>
				response(200).json(
					makeSearchResponse([makeSearchItem()], { expanded_with: ['faturamento', 'revenue'] })
				)
			)
		);
		render(LeftSidebar, { props: { onSelectTable: vi.fn() } });
		await fireEvent.input(screen.getByLabelText('Search tables and columns'), {
			target: { value: 'receita' }
		});
		expect(await screen.findByText('Also searched: faturamento, revenue')).toBeTruthy();
	});
});
