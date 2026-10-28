import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../test/server';
import { makeMe, makeMetadataCatalog, makeWorkspace } from '../../../test/factories';
import { clearMetadataCache } from '$lib/metadata/client';
import { authStore } from '$lib/stores/auth.svelte';
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

		function mockTables(runs: ReturnType<typeof run>[]) {
			server.use(
				http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
					response(200).json([makeMetadataCatalog({ id: CATALOG })])
				),
				http.get('/catalogs/{catalog_id}/table-health', ({ response }) =>
					response(200).json([
						{
							catalog_id: CATALOG,
							catalog_name: 'lake',
							namespace: 'sales',
							table_name: 'orders',
							health_score: 80,
							needs_compaction: false,
							needs_snapshot_expiry: false,
							needs_orphan_cleanup: false
						}
					])
				),
				http.get('/catalogs/{catalog_id}/maintenance-runs', ({ response }) =>
					response(200).json(runs)
				)
			);
		}

		it('lists running, finished and failed runs', async () => {
			mockTables([
				run('r1', 'running', 'orders'),
				run('r2', 'finished', 'customers'),
				run('r3', 'failed', 'events')
			]);
			render(MonitoringPage);
			await fireEvent.click(await screen.findByRole('tab', { name: 'Table health' }));

			expect(await screen.findByText('sales.customers')).toBeTruthy();
			expect(screen.getByText('sales.events')).toBeTruthy();
			expect(screen.getByText('engine_error')).toBeTruthy();
			expect(screen.queryByText('No maintenance runs yet.')).toBeNull();
		});

		it('shows the empty state for an empty list', async () => {
			mockTables([]);
			render(MonitoringPage);
			await fireEvent.click(await screen.findByRole('tab', { name: 'Table health' }));

			expect(await screen.findByText('No maintenance runs yet.')).toBeTruthy();
		});
	});
});
