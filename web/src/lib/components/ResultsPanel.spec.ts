import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http as mswHttp, HttpResponse } from 'msw';
import { tableFromArrays, tableToIPC } from 'apache-arrow';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../test/server';
import { makeWorkspace } from '../../test/factories';
import ResultsPanel from './ResultsPanel.svelte';

const WS = '00000000-0000-0000-0000-000000000001';
const EXEC = '00000000-0000-0000-0000-0000000000e1';

type ResultSet = { result_index: number; rows: number; bytes: number; truncated: boolean };

function statement(resultSets: ResultSet[]) {
	return {
		id: '00000000-0000-0000-0000-0000000000f1',
		execution_id: EXEC,
		position: 1,
		sql: 'SELECT 1',
		status: 'finished' as const,
		result_sets: resultSets
	};
}

function mockResult(rows: Record<string, unknown[]>) {
	const ipc = tableToIPC(tableFromArrays(rows), 'stream');
	const body = new Uint8Array(ipc.byteLength);
	body.set(ipc);
	server.use(
		mswHttp.get(
			'/api/v1/workspaces/:id/executions/:executionId/statements/:position/results',
			() =>
				new HttpResponse(body.buffer as ArrayBuffer, {
					status: 200,
					headers: { 'Content-Type': 'application/vnd.apache.arrow.stream' }
				})
		)
	);
}

describe('ResultsPanel', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		// Downloading in jsdom: object URLs and anchor navigation are stubs.
		Object.defineProperty(URL, 'createObjectURL', {
			value: vi.fn(() => 'blob:test'),
			configurable: true
		});
		Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn(), configurable: true });
		vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
	});

	it('streams the rows of the active statement', async () => {
		server.use(
			http.get('/workspaces/{id}/executions/{execution_id}/statements', ({ response }) =>
				response(200).json([
					statement([{ result_index: 0, rows: 2, bytes: 100, truncated: false }])
				])
			)
		);
		mockResult({ id: [1, 2], note: ['a', 'b'] });

		render(ResultsPanel, { props: { executionId: EXEC, enabled: true } });

		expect(await screen.findByText('id')).toBeTruthy();
		expect(await screen.findByText('note')).toBeTruthy();
		expect(await screen.findByText('a')).toBeTruthy();
		// A lone statement has no tab strip: the run panel already names it (#393).
		expect(screen.queryByRole('tab')).toBeNull();
	});

	it('shows one tab per statement for a script, without status badges', async () => {
		server.use(
			http.get('/workspaces/{id}/executions/{execution_id}/statements', ({ response }) =>
				response(200).json([
					statement([{ result_index: 0, rows: 2, bytes: 100, truncated: false }]),
					{
						...statement([{ result_index: 0, rows: 2, bytes: 100, truncated: false }]),
						id: '00000000-0000-0000-0000-0000000000f2',
						position: 2,
						sql: 'SELECT 2'
					}
				])
			)
		);
		mockResult({ id: [1, 2], note: ['a', 'b'] });

		render(ResultsPanel, { props: { executionId: EXEC, enabled: true } });

		const tabs = await screen.findAllByRole('tab');
		expect(tabs.map((tab) => tab.textContent?.trim())).toEqual(['Statement 1', 'Statement 2']);
		expect(screen.queryByText('Finished')).toBeNull();
	});

	it('warns when the result was truncated', async () => {
		server.use(
			http.get('/workspaces/{id}/executions/{execution_id}/statements', ({ response }) =>
				response(200).json([statement([{ result_index: 0, rows: 2, bytes: 100, truncated: true }])])
			)
		);
		mockResult({ id: [1, 2] });

		render(ResultsPanel, { props: { executionId: EXEC, enabled: true } });

		expect(await screen.findByText(/Showing the first 2 rows/)).toBeTruthy();
	});

	it('shows an empty state when the statement produced no result set', async () => {
		server.use(
			http.get('/workspaces/{id}/executions/{execution_id}/statements', ({ response }) =>
				response(200).json([statement([])])
			)
		);

		render(ResultsPanel, { props: { executionId: EXEC, enabled: true } });

		expect(await screen.findByText('No rows')).toBeTruthy();
	});

	it('shows an error state when the statements read fails', async () => {
		server.use(
			http.get('/workspaces/{id}/executions/{execution_id}/statements', ({ response }) =>
				response(404).json({ code: 'not_found', message: 'execution not found' })
			)
		);

		render(ResultsPanel, { props: { executionId: EXEC, enabled: true } });

		expect(await screen.findByText('Could not load the results')).toBeTruthy();
		// The UI translates the closed code instead of showing the raw body (#67).
		expect(await screen.findByText('The resource was not found.')).toBeTruthy();
	});

	it('exports the active result set in the chosen format', async () => {
		server.use(
			http.get('/workspaces/{id}/executions/{execution_id}/statements', ({ response }) =>
				response(200).json([
					statement([{ result_index: 0, rows: 1, bytes: 100, truncated: false }])
				])
			)
		);
		const ipc = tableToIPC(tableFromArrays({ id: [1] }), 'stream');
		const body = new Uint8Array(ipc.byteLength);
		body.set(ipc);
		let exportUrl = '';
		server.use(
			mswHttp.get(
				'/api/v1/workspaces/:id/executions/:executionId/statements/:position/results',
				({ request }) => {
					if (request.url.includes('format=')) exportUrl = request.url;
					return new HttpResponse(body.buffer as ArrayBuffer, {
						status: 200,
						headers: { 'Content-Type': 'application/vnd.apache.arrow.stream' }
					});
				}
			)
		);

		render(ResultsPanel, { props: { executionId: EXEC, enabled: true } });

		// Export sits in the grid toolbar once the result loads.
		await fireEvent.change(await screen.findByLabelText('Export format'), {
			target: { value: 'tsv' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Export' }));

		await waitFor(() => expect(exportUrl).toContain('format=tsv'));
		expect(exportUrl).toContain('result_index=0');
	});
});
