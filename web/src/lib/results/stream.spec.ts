import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setAccessToken } from '$lib/api/client';
import { http, server } from '../../test/server';
import { subscribeExecution, terminalStatus, type ExecutionEvent } from './stream';

const WS = '00000000-0000-0000-0000-000000000001';
const EXEC = '00000000-0000-0000-0000-0000000000e1';

class FakeSocket {
	static instances: FakeSocket[] = [];
	onopen: (() => void) | null = null;
	onmessage: ((event: { data: string }) => void) | null = null;
	onclose: (() => void) | null = null;

	constructor(readonly url: string) {
		FakeSocket.instances.push(this);
	}

	close() {
		this.onclose?.();
	}

	emit(event: ExecutionEvent) {
		this.onmessage?.({ data: JSON.stringify(event) });
	}
}

const failedSnapshot: ExecutionEvent = {
	type: 'snapshot',
	execution_id: EXEC,
	seq: 1,
	ts: '2026-01-01T00:00:00Z',
	execution: {
		id: EXEC,
		workspace_id: WS,
		status: 'failed',
		sql: 'SELECT * FROM tpch.sf1.customer',
		created_at: '2026-01-01T00:00:00Z'
	},
	statements: []
} as ExecutionEvent;

function ticketHandler() {
	server.use(
		http.post('/ws-tickets', ({ response }) =>
			response(201).json({ ticket: 'ticket-1', expires_at: '2030-01-01T00:00:00Z' })
		)
	);
}

describe('subscribeExecution', () => {
	beforeEach(() => {
		FakeSocket.instances = [];
		vi.stubGlobal('WebSocket', FakeSocket);
		setAccessToken('secret-jwt');
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		// Access tokens are memory-only; clearing local storage is irrelevant.
		setAccessToken(null);
	});

	it('connects with a single-use ticket, never the JWT', async () => {
		ticketHandler();
		const events: ExecutionEvent[] = [];
		const stream = subscribeExecution({
			workspaceId: WS,
			executionId: EXEC,
			onEvent: (event) => events.push(event)
		});

		await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(1));
		const url = FakeSocket.instances[0].url;
		expect(url).toContain('ticket=ticket-1');
		expect(url).not.toContain('secret-jwt');
		expect(url).not.toContain('token=');

		FakeSocket.instances[0].emit({
			type: 'execution_status',
			execution_id: EXEC,
			seq: 1,
			ts: '2026-01-01T00:00:00Z',
			status: 'running'
		});
		expect(events).toHaveLength(1);
		stream.close();
	});

	it('stops reconnecting after a terminal event', async () => {
		ticketHandler();
		const stream = subscribeExecution({
			workspaceId: WS,
			executionId: EXEC,
			onEvent: () => {}
		});

		await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(1));
		FakeSocket.instances[0].emit({
			type: 'execution_status',
			execution_id: EXEC,
			seq: 2,
			ts: '2026-01-01T00:00:00Z',
			status: 'finished'
		});
		FakeSocket.instances[0].close();

		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(FakeSocket.instances).toHaveLength(1);
		stream.close();
	});

	// #387: a statement that fails in a few ms is final before the socket opens;
	// the server sends only the snapshot and closes.
	it('ends on a snapshot of an execution that already failed', async () => {
		ticketHandler();
		const stream = subscribeExecution({
			workspaceId: WS,
			executionId: EXEC,
			onEvent: () => {}
		});

		await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(1));
		FakeSocket.instances[0].emit(failedSnapshot);
		FakeSocket.instances[0].close();

		await new Promise((resolve) => setTimeout(resolve, 700));
		expect(FakeSocket.instances).toHaveLength(1);
		stream.close();
	});

	it('reconnects with a new ticket after a dropped connection', async () => {
		ticketHandler();
		const stream = subscribeExecution({
			workspaceId: WS,
			executionId: EXEC,
			onEvent: () => {},
			maxAttempts: 2
		});

		await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(1));
		FakeSocket.instances[0].close();

		await vi.waitFor(() => expect(FakeSocket.instances).toHaveLength(2), { timeout: 2000 });
		stream.close();
	});
});

describe('terminalStatus', () => {
	it('reads the final status of an execution_status event and of a snapshot', () => {
		expect(terminalStatus(failedSnapshot)).toBe('failed');
		expect(
			terminalStatus({
				type: 'execution_status',
				execution_id: EXEC,
				seq: 2,
				ts: '2026-01-01T00:00:00Z',
				status: 'running'
			})
		).toBeNull();
		expect(
			terminalStatus({
				...failedSnapshot,
				execution: { ...(failedSnapshot as { execution: object }).execution, status: 'running' }
			} as ExecutionEvent)
		).toBeNull();
	});
});
