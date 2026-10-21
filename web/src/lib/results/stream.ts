// WebSocket execution stream (#26, #111): single-use tickets keep the JWT out
// of the URL, and a reconnect re-subscribes with backoff. The server sends a
// full snapshot first, so a missed event is harmless.
import { API_ROOT, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type ExecutionEvent = components['schemas']['ExecutionEvent'];

export type ExecutionStream = { close: () => void };

const TERMINAL = new Set(['finished', 'failed', 'cancelled']);

/**
 * The execution's final status when `event` ends the stream, else null. An
 * execution that ended before the socket opened (a statement failing in a few
 * ms) only gets a `snapshot`, after which the server closes (#387).
 */
export function terminalStatus(event: ExecutionEvent): string | null {
	const status =
		event.type === 'execution_status'
			? event.status
			: event.type === 'snapshot'
				? event.execution.status
				: '';
	return TERMINAL.has(status) ? status : null;
}

export function isTerminal(event: ExecutionEvent): boolean {
	return terminalStatus(event) !== null;
}

export function subscribeExecution(options: {
	workspaceId: string;
	executionId: string;
	onEvent: (event: ExecutionEvent) => void;
	onGaveUp?: () => void;
	maxAttempts?: number;
}): ExecutionStream {
	let closed = false;
	let terminal = false;
	let attempts = 0;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let socket: WebSocket | null = null;

	function scheduleReconnect() {
		if (closed || terminal) return;
		const max = options.maxAttempts ?? 5;
		if (attempts >= max) {
			options.onGaveUp?.();
			return;
		}
		const delay = Math.min(30_000, 500 * 2 ** attempts);
		attempts += 1;
		timer = setTimeout(() => void connect(), delay);
	}

	async function connect() {
		if (closed || terminal) return;
		const { data, error } = await client.POST('/ws-tickets', {
			body: { execution_id: options.executionId }
		});
		if (closed || terminal) return;
		if (error || !data) {
			scheduleReconnect();
			return;
		}

		const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
		socket = new WebSocket(
			`${proto}//${location.host}${API_ROOT}/ws/workspaces/${options.workspaceId}/executions/${options.executionId}?ticket=${encodeURIComponent(data.ticket)}`
		);
		socket.onopen = () => {
			attempts = 0;
		};
		socket.onmessage = ({ data: raw }) => {
			let event: ExecutionEvent;
			try {
				event = JSON.parse(String(raw)) as ExecutionEvent;
			} catch {
				return;
			}
			if (isTerminal(event)) terminal = true;
			options.onEvent(event);
		};
		socket.onclose = () => {
			if (closed || terminal) return;
			scheduleReconnect();
		};
	}

	void connect();

	return {
		close() {
			closed = true;
			if (timer) clearTimeout(timer);
			socket?.close();
			socket = null;
		}
	};
}
