import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { HttpResponse, http as mswHttp } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../../test/server';
import {
	makeMaintenancePlan,
	makeMe,
	makeMetadataCatalog,
	makeTableMaintenance,
	makeWorkspace
} from '../../../test/factories';
import { renderPage } from '../../../test/render';
import { clearMetadataCache } from '$lib/metadata/client';
import { authStore } from '$lib/stores/auth.svelte';
import { toasts } from '$lib/ui/toast.svelte';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import MonitoringPage from './+page.svelte';
import type { TableHealth } from '$lib/monitoring/client';

const ENGINE = '00000000-0000-0000-0000-0000000000e1';
const WORKSPACE = '00000000-0000-0000-0000-000000000001';

describe('monitoring page', () => {
	beforeEach(() => {
		clearMetadataCache();
		authStore.set(makeMe());
		workspaceStore.select(makeWorkspace({ id: WORKSPACE }));
		// Downloads in jsdom: object URLs and anchor navigation are stubs.
		Object.defineProperty(URL, 'createObjectURL', {
			value: vi.fn(() => 'blob:test'),
			configurable: true
		});
		Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn(), configurable: true });
		vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
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
			),
			// The live push stream (#733): stays open until the page aborts it.
			mswHttp.get(
				'/api/v1/engines/:id/monitoring/events',
				() =>
					new HttpResponse(new ReadableStream({ start() {} }), {
						status: 200,
						headers: { 'Content-Type': 'text/event-stream' }
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

	it('offers a five-minute window and pauses live updates', async () => {
		render(MonitoringPage);
		expect(await screen.findByText('Workers')).toBeTruthy();

		const window = screen.getByLabelText('Window');
		expect(within(window).getByRole('option', { name: 'Last 5 minutes' })).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Pause live updates' }));
		expect(screen.getByRole('button', { name: 'Resume live updates' })).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Resume live updates' }));
		expect(await screen.findByRole('button', { name: 'Pause live updates' })).toBeTruthy();
	});

	it('shows a health card per engine and selects one', async () => {
		const other = '00000000-0000-0000-0000-0000000000e2';
		server.use(
			http.get('/monitoring/engines', ({ response }) =>
				response(200).json([
					{ id: ENGINE, name: 'trino', kind: 'trino', class: 'federated' },
					{ id: other, name: 'trino-b', kind: 'trino', class: 'federated' }
				])
			),
			http.get('/engines/{id}/queues', ({ params, response }) =>
				response(200).json({
					engine_id: params.id,
					classes: [
						{
							class: 'interactive',
							max_running: 5,
							max_queued: 10,
							queue_timeout_ms: 1000,
							runtime_timeout_ms: 2000,
							running: 2,
							queued: 1,
							oldest_wait_ms: null
						}
					]
				})
			)
		);
		render(MonitoringPage);

		expect(await screen.findByRole('heading', { name: 'Engines' })).toBeTruthy();
		const second = await screen.findByRole('button', { name: /trino-b/ });
		expect(within(second).getByText('Healthy')).toBeTruthy();
		expect(within(second).getByText('Interactive: 2 running · 1 queued')).toBeTruthy();

		await fireEvent.click(second);
		await waitFor(() => expect(second.getAttribute('aria-pressed')).toBe('true'));
	});

	it('sorts the query page by duration', async () => {
		const base = {
			state: 'finished',
			user: 'ada',
			catalog: 'lake',
			created_at: '2026-10-01T12:00:00Z'
		};
		server.use(
			http.get('/engines/{id}/monitoring/queries', ({ response }) =>
				response(200).json({
					items: [
						{ ...base, trino_query_id: 'fast', sql_first_line: 'SELECT fast', elapsed_ms: 100 },
						{ ...base, trino_query_id: 'slow', sql_first_line: 'SELECT slow', elapsed_ms: 5000 }
					]
				})
			)
		);
		render(MonitoringPage);

		await fireEvent.click(await screen.findByRole('tab', { name: 'Queries' }));
		await screen.findByText('SELECT fast');
		await fireEvent.change(screen.getByLabelText('Sort by'), { target: { value: 'elapsed' } });

		const rows = screen.getAllByRole('row');
		expect(within(rows[1]).getByText('SELECT slow')).toBeTruthy();
	});

	it('filters Iceberg health by schema and opens the maintenance editor', async () => {
		const CATALOG = '00000000-0000-0000-0000-0000000000c1';
		const healthQueries: URLSearchParams[] = [];
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog({ id: CATALOG, origin: 'source' })])
			),
			http.get('/catalogs/{catalog_id}/table-health', ({ request, response }) => {
				healthQueries.push(new URL(request.url).searchParams);
				return response(200).json([
					{
						catalog_id: CATALOG,
						catalog_name: 'lake',
						namespace: 'sales',
						table_name: 'orders',
						health_score: 80,
						needs_compaction: false,
						needs_snapshot_expiry: false,
						due_operations: []
					}
				]);
			}),
			http.get('/catalogs/{catalog_id}/maintenance-runs', ({ response }) => response(200).json([])),
			http.get('/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance', ({ response }) =>
				response(200).json(makeTableMaintenance({ properties: {} }))
			)
		);
		render(MonitoringPage);

		await fireEvent.click(await screen.findByRole('tab', { name: 'Iceberg health' }));
		await screen.findByText('sales.orders');

		await fireEvent.input(screen.getByLabelText('Schema'), { target: { value: 'sales' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
		await waitFor(() => expect(healthQueries.at(-1)?.get('ns')).toBe('sales'));

		await fireEvent.click(screen.getByRole('button', { name: 'Configure' }));
		const dialog = await screen.findByRole('dialog');
		expect(await within(dialog).findByText(/has no maintenance parameters/)).toBeTruthy();
		expect(within(dialog).getByText('Optimize')).toBeTruthy();
	});

	it('lists the catalog maintenance plan and opens the editor', async () => {
		const CATALOG = '00000000-0000-0000-0000-0000000000c1';
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog({ id: CATALOG, origin: 'source' })])
			),
			http.get('/catalogs/{catalog_id}/maintenance-plan', ({ response }) =>
				response(200).json([
					{
						namespace: 'sales',
						table_name: 'orders',
						mode: 'managed',
						plan: makeMaintenancePlan(),
						last_run: null
					}
				])
			),
			http.get('/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance', ({ response }) =>
				response(200).json(makeTableMaintenance({ properties: {} }))
			)
		);
		render(MonitoringPage);

		await fireEvent.click(await screen.findByRole('tab', { name: 'Maintenance' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'sales.orders' }));

		const dialog = await screen.findByRole('dialog');
		expect(await within(dialog).findByText(/has no maintenance parameters/)).toBeTruthy();
	});

	it('exports every query page with the current filters', async () => {
		const row = (id: string) => ({
			trino_query_id: id,
			state: 'finished',
			user: 'ada',
			sql_first_line: `SELECT ${id}`,
			catalog: 'lake',
			created_at: '2026-10-01T12:00:00Z'
		});
		const requests: URLSearchParams[] = [];
		server.use(
			http.get('/engines/{id}/monitoring/queries', ({ request, response }) => {
				const params = new URL(request.url).searchParams;
				requests.push(params);
				const offset = Number(params.get('offset') ?? 0);
				return response(200).json(
					offset === 0
						? { items: [row('q1')], next_offset: 1 }
						: { items: [row('q2')], next_offset: null }
				);
			})
		);
		render(MonitoringPage);

		await fireEvent.click(await screen.findByRole('tab', { name: 'Queries' }));
		await screen.findByText('SELECT q1');
		await fireEvent.input(screen.getByLabelText('SQL contains'), {
			target: { value: 'orders' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }));

		await waitFor(() => expect(requests.some((params) => params.get('offset') === '1')).toBe(true));
		const exported = requests.filter((params) => params.get('q') === 'orders');
		expect(exported.map((params) => params.get('offset'))).toEqual(['0', '1']);
		expect(exported[0]?.get('limit')).toBe('100');
	});

	// #733: the server pushes a "changed" signal; the page re-fetches the data
	// instead of polling on a fixed timer.
	it('re-fetches the cluster when the push stream reports a change', async () => {
		let clusterCalls = 0;
		let push: ((chunk: string) => void) | undefined;
		server.use(
			http.get('/engines/{id}/monitoring/cluster', ({ response }) => {
				clusterCalls++;
				return response(200).json({
					latest: {
						captured_at: new Date().toISOString(),
						active_workers: 3,
						running_queries: 2,
						queued_queries: 0,
						blocked_queries: 0,
						total_memory_bytes: 100,
						free_memory_bytes: 40
					},
					series: []
				});
			}),
			mswHttp.get('/api/v1/engines/:id/monitoring/events', () => {
				const body = new ReadableStream({
					start(controller) {
						const enc = new TextEncoder();
						push = (chunk) => controller.enqueue(enc.encode(chunk));
					}
				});
				return new HttpResponse(body, {
					status: 200,
					headers: { 'Content-Type': 'text/event-stream' }
				});
			})
		);
		render(MonitoringPage);

		// The initial fetch completes and the stream opens...
		await waitFor(() => expect(clusterCalls).toBe(1));
		await waitFor(() => expect(push).toBeDefined());
		const before = clusterCalls;
		// ...then a pushed change re-fetches.
		push!('event: changed\ndata: {"kind":"cluster"}\n\n');
		await waitFor(() => expect(clusterCalls).toBeGreaterThan(before));
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
			workflow_id: `wf-${id}`,
			trigger: 'manual' as const,
			reason: {},
			attempts: 1
		});

		const healthRow = {
			catalog_id: CATALOG,
			catalog_name: 'lake',
			namespace: 'sales',
			table_name: 'orders',
			health_score: 80,
			needs_compaction: true,
			needs_snapshot_expiry: false,
			due_operations: [] as TableHealth['due_operations']
		};

		function mockTables(
			runs: ReturnType<typeof run>[],
			options: {
				health?: (Partial<TableHealth> & typeof healthRow)[];
				runsFail?: boolean;
				runQueries?: URLSearchParams[];
			} = {}
		) {
			server.use(
				http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
					response(200).json([makeMetadataCatalog({ id: CATALOG, origin: 'source' })])
				),
				http.get('/catalogs/{catalog_id}/table-health', ({ response }) =>
					response(200).json(options.health ?? [healthRow])
				),
				http.get('/catalogs/{catalog_id}/maintenance-runs', ({ request, response }) => {
					options.runQueries?.push(new URL(request.url).searchParams);
					return options.runsFail
						? response(404).json({ code: 'not_found', message: 'catalog not found' })
						: response(200).json(runs);
				})
			);
		}

		async function openTables() {
			renderPage(MonitoringPage, { me: makeMe(), url: '/monitoring' });
			await fireEvent.click(await screen.findByRole('tab', { name: 'Iceberg health' }));
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

		it('shows what parameters find due and links each table to its configuration', async () => {
			mockTables([], {
				health: [
					{
						...healthRow,
						due_operations: ['optimize', 'expire_snapshots'],
						maintenance_mode: 'managed'
					}
				]
			});
			await openTables();

			const row = (await screen.findByText('sales.orders')).closest('tr')!;
			expect(within(row).getByText('Optimize')).toBeTruthy();
			expect(within(row).getByText('Expire snapshots')).toBeTruthy();
			expect(within(row).getByText('Managed')).toBeTruthy();
			expect(within(row).getByRole('button', { name: 'Configure' })).toBeTruthy();
			expect(within(row).getByRole('button', { name: 'Run now' })).toBeTruthy();
			expect(within(row).queryByRole('button', { name: 'Run maintenance…' })).toBeNull();
		});

		it('filters the run history by trigger', async () => {
			const runQueries: URLSearchParams[] = [];
			mockTables([], { runQueries });
			await openTables();
			await screen.findByText('sales.orders');

			await fireEvent.change(screen.getByLabelText('Trigger'), { target: { value: 'automatic' } });
			await waitFor(() => expect(runQueries.at(-1)?.get('trigger')).toBe('automatic'));
		});

		it('offers to analyze a catalog that has no health rows yet', async () => {
			let analyzed = false;
			mockTables([], { health: [] });
			server.use(
				http.post('/catalogs/{catalog_id}/table-health/analyze', ({ response }) => {
					analyzed = true;
					return response(202).json({ analysis_id: 'analysis-1' });
				})
			);
			await openTables();

			await screen.findByRole('button', { name: 'Analyze now' });
			// The empty state is rebuilt when health reloads; click the live one.
			await waitFor(async () => {
				await fireEvent.click(screen.getByRole('button', { name: 'Analyze now' }));
				expect(analyzed).toBe(true);
			});
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
			due_operations: [] as TableHealth['due_operations']
		});
		let releaseB: () => void = () => {};
		const gateB = new Promise<void>((resolve) => (releaseB = resolve));
		const posted: string[] = [];
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([
					makeMetadataCatalog({ id: A, name: 'lake', origin: 'source' }),
					makeMetadataCatalog({ id: B, name: 'warehouse', origin: 'source' })
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
					workflow_id: 'wf-1',
					trigger: 'manual' as const,
					reason: {},
					attempts: 1
				});
			})
		);
		render(MonitoringPage);
		await fireEvent.click(await screen.findByRole('tab', { name: 'Iceberg health' }));
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
			due_operations: [] as TableHealth['due_operations']
		});
		let releaseA: () => void = () => {};
		const gateA = new Promise<void>((resolve) => (releaseA = resolve));
		const posted: string[] = [];
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([
					makeMetadataCatalog({ id: A, name: 'lake', origin: 'source' }),
					makeMetadataCatalog({ id: B, name: 'warehouse', origin: 'source' })
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
					workflow_id: 'wf-1',
					trigger: 'manual' as const,
					reason: {},
					attempts: 1
				});
			})
		);
		render(MonitoringPage);
		await fireEvent.click(await screen.findByRole('tab', { name: 'Iceberg health' }));
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
	// #480: the drawer opens at once, shows a failure with a retry, and a
	// slow first response never replaces the query clicked after it.
	describe('query detail drawer', () => {
		const listed = (id: string, sql: string) => ({
			trino_query_id: id,
			state: 'finished',
			user: 'ada',
			sql_first_line: sql,
			catalog: 'lake',
			created_at: '2026-10-01T12:00:00Z'
		});

		function useQueries(detail: (id: string) => Promise<Response | null>) {
			server.use(
				http.get('/engines/{id}/monitoring/queries', ({ response }) =>
					response(200).json({
						items: [listed('q1', 'SELECT 1'), listed('q2', 'SELECT 2')]
					})
				),
				http.get('/engines/{id}/monitoring/queries/{query_id}', async ({ params, response }) => {
					const result = await detail(params.query_id);
					if (result) return result;
					return response(200).json({
						...listed(params.query_id, ''),
						sql: `SELECT detail_${params.query_id}`
					});
				})
			);
		}

		it('shows a loading state, then an error with a retry', async () => {
			let release: () => void = () => {};
			const gate = new Promise<void>((resolve) => (release = resolve));
			let failing = true;
			useQueries(async () => {
				await gate;
				return failing
					? new Response(JSON.stringify({ code: 'not_found', message: 'gone' }), {
							status: 404,
							headers: { 'content-type': 'application/json' }
						})
					: null;
			});
			render(MonitoringPage);
			await fireEvent.click(await screen.findByRole('tab', { name: 'Queries' }));
			await fireEvent.click(await screen.findByText('SELECT 1'));

			expect(await screen.findByRole('status', { name: 'Loading the query detail' })).toBeTruthy();
			release();
			expect(await screen.findByText('Could not load the query')).toBeTruthy();

			failing = false;
			await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
			expect(await screen.findByText('SELECT detail_q1')).toBeTruthy();
		});

		it('keeps the last clicked query when an earlier answer is late', async () => {
			let releaseFirst: () => void = () => {};
			const first = new Promise<void>((resolve) => (releaseFirst = resolve));
			useQueries(async (id) => {
				if (id === 'q1') await first;
				return null;
			});
			render(MonitoringPage);
			await fireEvent.click(await screen.findByRole('tab', { name: 'Queries' }));
			await fireEvent.click(await screen.findByText('SELECT 1'));
			await fireEvent.click(screen.getByText('SELECT 2'));
			expect(await screen.findByText('SELECT detail_q2')).toBeTruthy();

			releaseFirst();
			await new Promise((resolve) => setTimeout(resolve, 20));
			expect(screen.getByText('SELECT detail_q2')).toBeTruthy();
			expect(screen.queryByText('SELECT detail_q1')).toBeNull();
		});
	});

	// #481: Retention shows only for operations that use it, and is sent only then.
	it('asks for retention only with expiry or orphan cleanup', async () => {
		const CATALOG = '00000000-0000-0000-0000-0000000000c1';
		const bodies: { operations: string[]; retention?: string }[] = [];
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog({ id: CATALOG, origin: 'source' })])
			),
			http.get('/catalogs/{catalog_id}/table-health', ({ response }) =>
				response(200).json([
					{
						catalog_id: CATALOG,
						catalog_name: 'lake',
						namespace: 'sales',
						table_name: 'orders',
						health_score: 40,
						needs_compaction: true,
						needs_snapshot_expiry: false,
						due_operations: []
					}
				])
			),
			http.get('/catalogs/{catalog_id}/maintenance-runs', ({ response }) => response(200).json([])),
			http.post('/catalogs/{catalog_id}/maintenance', async ({ request, response }) => {
				const body = (await request.json()) as { operations: string[]; retention?: string };
				bodies.push(body);
				return response(202).json({
					id: `run-${bodies.length}`,
					catalog_id: CATALOG,
					namespace: 'sales',
					table_name: 'orders',
					operations: body.operations,
					status: 'finished',
					started_at: '2026-10-01T12:00:00Z',
					finished_at: '2026-10-01T12:01:00Z',
					workflow_id: 'wf',
					trigger: 'manual' as const,
					reason: {},
					attempts: 1
				});
			})
		);
		render(MonitoringPage);
		await fireEvent.click(await screen.findByRole('tab', { name: 'Iceberg health' }));

		await fireEvent.click(await screen.findByRole('button', { name: 'Run maintenance…' }));
		let dialog = await screen.findByRole('dialog');
		expect(within(dialog).queryByLabelText('Retention (e.g. 7d, 30d)')).toBeNull();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Run maintenance' }));
		await waitFor(() => expect(bodies).toHaveLength(1));
		expect(bodies[0].retention).toBeUndefined();

		await fireEvent.click(await screen.findByRole('button', { name: 'Run maintenance…' }));
		dialog = await screen.findByRole('dialog');
		await fireEvent.click(within(dialog).getByLabelText(/Expire snapshots/));
		const retention = within(dialog).getByLabelText('Retention (e.g. 7d, 30d)');
		await fireEvent.input(retention, { target: { value: '30d' } });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Run maintenance' }));
		await waitFor(() => expect(bodies).toHaveLength(2));
		expect(bodies[1]).toMatchObject({
			operations: ['optimize', 'expire_snapshots'],
			retention: '30d'
		});
	});
});
