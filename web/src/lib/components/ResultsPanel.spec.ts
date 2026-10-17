import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
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

		expect(await screen.findByText('Statement 1')).toBeTruthy();
		expect(await screen.findByText('id')).toBeTruthy();
		expect(await screen.findByText('note')).toBeTruthy();
		expect(await screen.findByText('a')).toBeTruthy();
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
		expect(await screen.findByText('execution not found')).toBeTruthy();
	});
});
