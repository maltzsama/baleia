import { describe, expect, it } from 'vitest';
import type { ExecutionEvent } from '$lib/results/stream';
import { applyEvent, initialRunState, orderedStatements } from './reducer';

const snapshot = {
	type: 'snapshot',
	execution_id: 'e1',
	seq: 1,
	ts: '2026-01-01T00:00:00Z',
	execution: {
		id: 'e1',
		workspace_id: 'w1',
		status: 'running',
		sql: 'SELECT 1; SELECT 2',
		created_at: '2026-01-01T00:00:00Z',
		read_only: false
	},
	statements: [
		{ id: 's1', execution_id: 'e1', position: 1, sql: 'SELECT 1', status: 'running' },
		{ id: 's2', execution_id: 'e1', position: 2, sql: 'SELECT 2', status: 'pending' }
	]
} as ExecutionEvent;

describe('run reducer', () => {
	it('replaces the state from a snapshot and keeps a monotonic seq', () => {
		const state = applyEvent(initialRunState('e1'), snapshot);
		expect(state.status).toBe('running');
		expect(state.lastSeq).toBe(1);
		expect(state.statements[1].status).toBe('running');
		expect(state.statements[2].status).toBe('pending');
		expect(orderedStatements(state).map((s) => s.position)).toEqual([1, 2]);
	});

	it('applies statement_status once and ignores a duplicate seq', () => {
		let state = applyEvent(initialRunState('e1'), snapshot);
		const finished = {
			type: 'statement_status',
			execution_id: 'e1',
			seq: 2,
			ts: '2026-01-01T00:00:01Z',
			position: 1,
			status: 'finished',
			rows_returned: 3,
			result_count: 1,
			truncated: false,
			duration_ms: 12
		} as ExecutionEvent;

		state = applyEvent(state, finished);
		expect(state.statements[1].status).toBe('finished');
		expect(state.statements[1].rowsReturned).toBe(3);
		expect(state.statements[1].durationMs).toBe(12);

		const again = applyEvent(state, finished);
		expect(again).toBe(state);
	});

	it('applies execution_status and an interrupted statement code', () => {
		let state = applyEvent(initialRunState('e1'), snapshot);
		state = applyEvent(state, {
			type: 'statement_status',
			execution_id: 'e1',
			seq: 2,
			ts: '2026-01-01T00:00:01Z',
			position: 1,
			status: 'failed',
			error_code: 'interrupted',
			error_message: 'worker stopped',
			result_count: 0,
			truncated: false
		} as ExecutionEvent);
		state = applyEvent(state, {
			type: 'execution_status',
			execution_id: 'e1',
			seq: 3,
			ts: '2026-01-01T00:00:02Z',
			status: 'failed'
		} as ExecutionEvent);

		expect(state.status).toBe('failed');
		expect(state.statements[1].errorCode).toBe('interrupted');
	});
});
