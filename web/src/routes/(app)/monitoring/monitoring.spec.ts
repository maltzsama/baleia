import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../test/server';
import { makeMe, makeMetadataCatalog, makeWorkspace } from '../../../test/factories';
import { renderPage } from '../../../test/render';
import { clearMetadataCache } from '$lib/metadata/client';
import { authStore } from '$lib/stores/auth.svelte';
import { toasts } from '$lib/ui/toast.svelte';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import MonitoringPage from './+page.svelte';

const ENGINE = '00000000-0000-0000-0000-0000000000e1';
const WORKSPACE = '00000000-0000-0000-0000-000000000001';

describe('monitoring page', () => {
	beforeEach(() => {
		clearMetadataCache();
		authStore.set(makeMe());
		workspaceStore.select(makeWorkspace({ id: WORKSPACE }));
		server.use(
			http.get('/workspaces', ({ response }) =>
				response(200).json([makeWorkspace({ id: WORKSPACE })])
			),
			http.get('/monitoring/engines', ({ response }) =>
				response(200).json([{ id: ENGINE, name: 'trino', kind: 'trino', class: 'federated' }])
			),
			http.get('/engines/{id}/monitoring/cluster', ({ response }) =>
				response(200).json({
					latest: {
						captured_at: new Date().toISOString(),
						active_workers: 3,
						running_queries: 2,
						queued_queries: 0,
						blocked_queries: 0,
						total_memory_bytes: 100,
						free_memory_bytes: 40
					},
					series: [
						{
							captured_at: new Date().toISOString(),
							active_workers: 3,
							running_queries: 2,
							queued_queries: 0,
							blocked_queries: 0
						}
					]
				})
			)
		);
	});

	it('renders the cluster tiles and hides the engine selector with one engine', async () => {
		render(MonitoringPage);

		expect(await screen.findByText('Workers')).toBeTruthy();
		expect(screen.getByText('Running')).toBeTruthy();
		await waitFor(() => expect(screen.getByText('3')).toBeTruthy());
		expect(screen.queryByLabelText('Engine')).toBeNull();
	});

	// #407: the query detail shows what the query used, from the list's
	// figures and the engine's report; unknown figures are left out.
	it("shows a query's resources in its detail", async () => {
		const query = {
			trino_query_id: 'q1',
			state: 'finished',
			user: 'ada',
			sql_first_line: 'SELECT 1',
			catalog: 'lake',
			created_at: '2026-10-01T12:00:00Z',
			elapsed_ms: 1500,
			cpu_ms: 840,
			physical_input_bytes: 2048,
			output_rows: 10
		};
		server.use(
			http.get('/engines/{id}/monitoring/queries', ({ response }) =>
				response(200).json({ items: [query] })
			),
			http.get('/engines/{id}/monitoring/queries/{query_id}', ({ response }) =>
				response(200).json({
					...query,
					sql: 'SELECT 1',
					resources: {
						peak_memory_bytes: 1572864,
						processed_rows: 1200,
						processed_bytes: null,
						output_rows: 10,
						output_bytes: 469,
						queued_ms: 12,
						planning_ms: null
					}
				})
			)
		);
		render(MonitoringPage);

		await fireEvent.click(await screen.findByRole('tab', { name: 'Queries' }));
		await fireEvent.click(await screen.findByText('SELECT 1'));

		const usage = await screen.findByLabelText('Resources');
		const shown = (label: string) =>
			within(usage).getByText(label).nextElementSibling?.textContent?.trim();
		expect(shown('CPU time')).toBe('840 ms');
		expect(shown('Peak memory')).toBe('1.5 MiB');
		expect(shown('Data read')).toBe('2.0 KiB');
		expect(shown('Rows processed')).toBe((1200).toLocaleString());
		expect(shown('Bytes returned')).toBe('469 B');
		// Unknown figures are not shown as zero.
		expect(within(usage).queryByText('Planning')).toBeNull();
		expect(within(usage).queryByText('Bytes processed')).toBeNull();
	});
	// #453: the runs the API returns are the ones the table lists.
	describe('maintenance runs', () => {
		const CATALOG = '00000000-0000-0000-0000-0000000000c1';
		const run = (id: string, status: string, table: string) => ({
			id,
			catalog_id: CATALOG,
			namespace: 'sales',
			table_name: table,
			operations: ['optimize'],
			status,
			started_at: '2026-10-01T12:00:00Z',
			finished_at: status === 'running' ? null : '2026-10-01T12:05:00Z',
			error_code: status === 'failed' ? 'engine_error' : null,
			workflow_id: `wf-${id}`
		});

		const healthRow = {
			catalog_id: CATALOG,
			catalog_name: 'lake',
			namespace: 'sales',
			table_name: 'orders',
			health_score: 80,
			needs_compaction: true,
			needs_snapshot_expiry: false,
			needs_orphan_cleanup: false
		};

		function mockTables(
			runs: ReturnType<typeof run>[],
			options: { health?: (typeof healthRow)[]; runsFail?: boolean } = {}
		) {
			server.use(
				http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
					response(200).json([makeMetadataCatalog({ id: CATALOG })])
				),
				http.get('/catalogs/{catalog_id}/table-health', ({ response }) =>
					response(200).json(options.health ?? [healthRow])
				),
				http.get('/catalogs/{catalog_id}/maintenance-runs', ({ response }) =>
					options.runsFail
						? response(404).json({ code: 'not_found', message: 'catalog not found' })
						: response(200).json(runs)
				)
			);
		}

		async function openTables() {
			renderPage(MonitoringPage, { me: makeMe(), url: '/monitoring' });
			await fireEvent.click(await screen.findByRole('tab', { name: 'Table health' }));
		}

		it('lists running, finished and failed runs', async () => {
			mockTables([
				run('r1', 'running', 'orders'),
				run('r2', 'finished', 'customers'),
				run('r3', 'failed', 'events')
			]);
			await openTables();

			expect(await screen.findByText('sales.customers')).toBeTruthy();
			expect(screen.getByText('sales.events')).toBeTruthy();
			expect(screen.getByText('engine_error')).toBeTruthy();
			expect(screen.queryByText('No maintenance runs yet.')).toBeNull();
		});

		it('lists the runs when no table has health rows', async () => {
			mockTables([run('r2', 'finished', 'customers')], { health: [] });
			await openTables();

			expect(await screen.findByText('sales.customers')).toBeTruthy();
			expect(screen.getByText('No table health rows')).toBeTruthy();
		});

		it('shows the empty state for an empty list', async () => {
			mockTables([]);
			await openTables();

			expect(await screen.findByText('No maintenance runs yet.')).toBeTruthy();
		});

		it('shows the error, not the empty state, when the runs request fails', async () => {
			mockTables([], { runsFail: true });
			await openTables();

			expect(await screen.findByText('Could not load table health')).toBeTruthy();
			expect(screen.queryByText('No maintenance runs yet.')).toBeNull();
		});

		it('lists a run started from the modal without a reload', async () => {
			let listed: ReturnType<typeof run>[] = [];
			mockTables([]);
			server.use(
				http.get('/catalogs/{catalog_id}/maintenance-runs', ({ response }) =>
					response(200).json(listed)
				),
				http.post('/catalogs/{catalog_id}/maintenance', ({ response }) => {
					listed = [run('r9', 'finished', 'orders')];
					return response(202).json(run('r9', 'running', 'orders'));
				})
			);
			await openTables();
			expect(await screen.findByText('No maintenance runs yet.')).toBeTruthy();

			await fireEvent.click(await screen.findByRole('button', { name: 'Run maintenance…' }));
			const dialog = await screen.findByRole('dialog');
			await fireEvent.click(within(dialog).getByRole('button', { name: 'Run maintenance' }));

			await waitFor(() => expect(screen.queryByText('No maintenance runs yet.')).toBeNull());
			expect(screen.getAllByText('sales.orders').length).toBeGreaterThan(1);
		});
	});

	// #458: a slow kill is sent once however often Kill query is pressed, and a
	// refusal stays in the dialog.
	describe('kill request', () => {
		const running = {
			trino_query_id: 'q9',
			state: 'running',
			user: 'ada',
			sql_first_line: 'SELECT * FROM orders',
			catalog: 'lake',
			created_at: '2026-10-01T12:00:00Z'
		};

		async function confirmKill() {
			render(MonitoringPage);
			await fireEvent.click(await screen.findByRole('tab', { name: 'Queries' }));
			await fireEvent.click(await screen.findByRole('button', { name: 'Kill' }));
			const dialog = await screen.findByRole('dialog');
			return { dialog, confirm: within(dialog).getByRole('button', { name: 'Kill query' }) };
		}

		it('sends one request while a slow kill is pending', async () => {
			let kills = 0;
			let release: () => void = () => {};
			const gate = new Promise<void>((resolve) => (release = resolve));
			server.use(
				http.get('/engines/{id}/monitoring/queries', ({ response }) =>
					response(200).json({ items: [running] })
				),
				http.post('/engines/{id}/monitoring/queries/{query_id}', async ({ response }) => {
					kills++;
					await gate;
					return response(202).empty();
				})
			);
			const { confirm } = await confirmKill();
			await fireEvent.click(confirm);
			await fireEvent.click(confirm);
			await fireEvent.click(confirm);
			release();
			await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
			expect(kills).toBe(1);
			expect(toasts.items.map((toast) => toast.message)).toContain(
				'Cancellation requested; the query stops shortly.'
			);
		});

		it('keeps a refused kill in the dialog', async () => {
			server.use(
				http.get('/engines/{id}/monitoring/queries', ({ response }) =>
					response(200).json({ items: [running] })
				),
				http.post('/engines/{id}/monitoring/queries/{query_id}', ({ response }) =>
					response(409).json({ code: 'conflict', message: 'the query already ended' })
				)
			);
			const { dialog, confirm } = await confirmKill();
			await fireEvent.click(confirm);
			// The API error is shown through the error catalog (FD7), in the dialog.
			await waitFor(() => expect(dialog.textContent).toContain('The resource already exists.'));
			expect(screen.getByRole('dialog')).toBeTruthy();
		});
	});

	// #458: Kill asks first, names the query and only the confirm kills it.
	it('kills a query only after confirmation', async () => {
		const query = {
			trino_query_id: 'q9',
			state: 'running',
			user: 'ada',
			sql_first_line: 'SELECT * FROM orders',
			catalog: 'lake',
			created_at: '2026-10-01T12:00:00Z'
		};
		let kills = 0;
		server.use(
			http.get('/engines/{id}/monitoring/queries', ({ response }) =>
				response(200).json({ items: [query] })
			),
			http.post('/engines/{id}/monitoring/queries/{query_id}', ({ response }) => {
				kills++;
				return response(202).empty();
			})
		);
		render(MonitoringPage);
		await fireEvent.click(await screen.findByRole('tab', { name: 'Queries' }));

		await fireEvent.click(await screen.findByRole('button', { name: 'Kill' }));
		const dialog = await screen.findByRole('dialog');
		expect(within(dialog).getByText('Kill this query?')).toBeTruthy();
		expect(
			within(dialog).getByText(
				'“SELECT * FROM orders” is stopped on trino, and ada gets an error instead of its results.'
			)
		).toBeTruthy();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
		expect(kills).toBe(0);

		await fireEvent.click(screen.getByRole('button', { name: 'Kill' }));
		const again = await screen.findByRole('dialog');
		await fireEvent.click(within(again).getByRole('button', { name: 'Kill query' }));
		await waitFor(() => expect(kills).toBe(1));
	});

	// #457: rows of the previous catalog never stay actionable, and a run
	// always targets the catalog its row came from.
	it('drops the previous catalog rows and targets the row catalog', async () => {
		const A = '00000000-0000-0000-0000-0000000000a1';
		const B = '00000000-0000-0000-0000-0000000000b1';
		const row = (catalog: string, name: string, table: string) => ({
			catalog_id: catalog,
			catalog_name: name,
			namespace: 'sales',
			table_name: table,
			health_score: 40,
			needs_compaction: true,
			needs_snapshot_expiry: false,
			needs_orphan_cleanup: false
		});
		let releaseB: () => void = () => {};
		const gateB = new Promise<void>((resolve) => (releaseB = resolve));
		const posted: string[] = [];
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([
					makeMetadataCatalog({ id: A, name: 'lake' }),
					makeMetadataCatalog({ id: B, name: 'warehouse' })
				])
			),
			http.get('/catalogs/{catalog_id}/table-health', async ({ params, response }) => {
				if (params.catalog_id === B) {
					await gateB;
					return response(200).json([row(B, 'warehouse', 'invoices')]);
				}
				return response(200).json([row(A, 'lake', 'orders')]);
			}),
			http.get('/catalogs/{catalog_id}/maintenance-runs', ({ response }) => response(200).json([])),
			http.post('/catalogs/{catalog_id}/maintenance', async ({ params, request, response }) => {
				posted.push(params.catalog_id);
				const body = (await request.json()) as { namespace: string; table: string };
				return response(202).json({
					id: 'run-1',
					catalog_id: params.catalog_id,
					namespace: body.namespace,
					table_name: body.table,
					operations: ['optimize'],
					status: 'finished',
					started_at: '2026-10-01T12:00:00Z',
					finished_at: '2026-10-01T12:01:00Z',
					workflow_id: 'wf-1'
				});
			})
		);
		render(MonitoringPage);
		await fireEvent.click(await screen.findByRole('tab', { name: 'Table health' }));
		expect(await screen.findByText('sales.orders')).toBeTruthy();

		// While B loads, no row of A is left to act on.
		await fireEvent.change(screen.getByLabelText('Catalog'), { target: { value: B } });
		await waitFor(() => expect(screen.queryByText('sales.orders')).toBeNull());
		expect(screen.queryByRole('button', { name: 'Run maintenance…' })).toBeNull();

		releaseB();
		expect(await screen.findByText('sales.invoices')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Run maintenance…' }));
		const dialog = await screen.findByRole('dialog');
		expect(within(dialog).getByText('warehouse.sales.invoices')).toBeTruthy();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Run maintenance' }));
		await waitFor(() => expect(posted).toEqual([B]));
	});

	it('ignores a late health response from the previous catalog', async () => {
		const A = '00000000-0000-0000-0000-0000000000a1';
		const B = '00000000-0000-0000-0000-0000000000b1';
		const row = (catalog: string, name: string) => ({
			catalog_id: catalog,
			catalog_name: name,
			namespace: 'sales',
			table_name: 'orders',
			health_score: 40,
			needs_compaction: true,
			needs_snapshot_expiry: false,
			needs_orphan_cleanup: false
		});
		let releaseA: () => void = () => {};
		const gateA = new Promise<void>((resolve) => (releaseA = resolve));
		const posted: string[] = [];
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([
					makeMetadataCatalog({ id: A, name: 'lake' }),
					makeMetadataCatalog({ id: B, name: 'warehouse' })
				])
			),
			http.get('/catalogs/{catalog_id}/table-health', async ({ params, response }) => {
				if (params.catalog_id === A) {
					await gateA;
					return response(200).json([row(A, 'lake')]);
				}
				return response(200).json([row(B, 'warehouse')]);
			}),
			http.get('/catalogs/{catalog_id}/maintenance-runs', ({ response }) => response(200).json([])),
			http.post('/catalogs/{catalog_id}/maintenance', async ({ params, request, response }) => {
				posted.push(params.catalog_id);
				const body = (await request.json()) as { namespace: string; table: string };
				return response(202).json({
					id: 'run-1',
					catalog_id: params.catalog_id,
					namespace: body.namespace,
					table_name: body.table,
					operations: ['optimize'],
					status: 'finished',
					started_at: '2026-10-01T12:00:00Z',
					finished_at: '2026-10-01T12:01:00Z',
					workflow_id: 'wf-1'
				});
			})
		);
		render(MonitoringPage);
		await fireEvent.click(await screen.findByRole('tab', { name: 'Table health' }));
		await screen.findByRole('option', { name: 'warehouse' });
		await fireEvent.change(screen.getByLabelText('Catalog'), { target: { value: B } });
		expect(await screen.findByText('sales.orders')).toBeTruthy();

		// The page shows nothing once A's rows are dropped, so give the late
		// response a bounded moment to land before checking the target.
		releaseA();
		await new Promise((resolve) => setTimeout(resolve, 50));
		await fireEvent.click(screen.getByRole('button', { name: 'Run maintenance…' }));
		const dialog = await screen.findByRole('dialog');
		expect(within(dialog).getByText('warehouse.sales.orders')).toBeTruthy();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Run maintenance' }));
		await waitFor(() => expect(posted).toEqual([B]));
	});
});
