import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../test/server';
import { makeWorkspace } from '../../../test/factories';
import HistoryPage from './+page.svelte';

const WS = '00000000-0000-0000-0000-000000000001';
const EXEC = '00000000-0000-0000-0000-0000000000e1';

function makeExecution(overrides: Record<string, unknown> = {}) {
	return {
		id: EXEC,
		workspace_id: WS,
		status: 'finished' as const,
		sql: 'SELECT * FROM orders',
		started_at: '2026-10-01T12:00:00Z',
		finished_at: '2026-10-01T12:00:01Z',
		rows_returned: 42,
		duration_ms: 120,
		user_id: '00000000-0000-0000-0000-000000000003',
		user_name: 'Ada Lovelace',
		target: {
			engine_id: '00000000-0000-0000-0000-000000000004',
			binding_id: '00000000-0000-0000-0000-0000000000b1',
			catalog_id: '00000000-0000-0000-0000-0000000000c1',
			engine_kind: 'trino',
			engine_name: 'trino',
			catalog_name: 'lake',
			engine_catalog: 'lake_t',
			schema: 'sales',
			read_only: false
		},
		...overrides
	};
}

describe('history page', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		window.history.replaceState(null, '', '/history');
	});

	it('renders the executions with user, engine and SQL', async () => {
		server.use(
			http.get('/workspaces/{id}/executions', ({ response }) =>
				response(200).json({ data: [makeExecution()], next_cursor: null })
			)
		);

		render(HistoryPage);

		expect(await screen.findByText('Ada Lovelace')).toBeTruthy();
		expect(screen.getByText('SELECT * FROM orders')).toBeTruthy();
		expect(screen.getByText('42')).toBeTruthy();
		expect(screen.getByText('lake.sales')).toBeTruthy();
	});

	it('opens the drawer with the statements', async () => {
		server.use(
			http.get('/workspaces/{id}/executions', ({ response }) =>
				response(200).json({ data: [makeExecution()], next_cursor: null })
			),
			http.get('/workspaces/{id}/executions/{execution_id}/statements', ({ response }) =>
				response(200).json([
					{
						id: '00000000-0000-0000-0000-0000000000f1',
						execution_id: EXEC,
						position: 1,
						sql: 'SELECT 1',
						status: 'finished',
						duration_ms: 12,
						rows_returned: 1,
						result_sets: [{ result_index: 0, rows: 1, bytes: 10, truncated: false }]
					}
				])
			)
		);

		render(HistoryPage);
		await fireEvent.click(await screen.findByText('SELECT * FROM orders'));

		expect(await screen.findByText('Execution details')).toBeTruthy();
		expect(await screen.findByText('#1')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Re-run' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Results' })).toBeTruthy();
	});
});
