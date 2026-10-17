// WebSocket execution stream (#26): single-use tickets keep the JWT out of the
// URL, and a reconnect re-subscribes with backoff. The server sends the current
// execution state on connect, so a missed event is harmless.
import { API_ROOT, client } from '$lib/api/client';

export type ExecutionEvent = {
	type: string;
	execution_id?: string;
	statement_position?: number;
	rows_so_far?: number;
	rows_returned?: number;
	duration_ms?: number;
	message?: string;
	result_url?: string;
	ts?: string;
};

export type ExecutionStream = { close: () => void };

const TERMINAL = new Set(['finished', 'failed', 'cancelled']);

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
			if (TERMINAL.has(event.type)) terminal = true;
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
