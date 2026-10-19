// SSE over fetch (#63): EventSource cannot send the Authorization header, and
// the token must never go into the URL. The parser follows the SSE spec:
// blank-line separated frames, multi-line data, CRLF tolerated, comments
// ignored.
import { API_ROOT } from '$lib/api/base';
import { getAccessToken, refresh } from '$lib/auth/session';

export type SseFrame = { event: string; data: string; id?: string };

/** parseFrame parses one frame (without the trailing blank line). */
export function parseFrame(raw: string): SseFrame | null {
	let event = 'message';
	let id: string | undefined;
	const data: string[] = [];
	for (const line of raw.split(/\r?\n/)) {
		if (line === '' || line.startsWith(':')) continue;
		const colon = line.indexOf(':');
		const field = colon === -1 ? line : line.slice(0, colon);
		let value = colon === -1 ? '' : line.slice(colon + 1);
		if (value.startsWith(' ')) value = value.slice(1);
		if (field === 'event') event = value;
		else if (field === 'id') id = value;
		else if (field === 'data') data.push(value);
	}
	if (data.length === 0 && event === 'message' && id === undefined) return null;
	return { event, data: data.join('\n'), id };
}

/** parseSse splits a complete payload into frames (used by tests). */
export function parseSse(payload: string): SseFrame[] {
	const frames: SseFrame[] = [];
	for (const block of payload.split(/\r?\n\r?\n/)) {
		if (block.trim() === '') continue;
		const frame = parseFrame(block);
		if (frame) frames.push(frame);
	}
	return frames;
}

/** streamEvents connects to an SSE endpoint and yields frames until abort. */
export async function streamEvents(
	path: string,
	options: {
		signal: AbortSignal;
		onEvent: (frame: SseFrame) => void;
		onOpen?: () => void;
	}
): Promise<void> {
	const connect = async (): Promise<Response> => {
		const token = getAccessToken();
		return fetch(`${API_ROOT}${path}`, {
			headers: {
				Accept: 'text/event-stream',
				'X-Requested-With': 'baleia',
				...(token ? { Authorization: `Bearer ${token}` } : {})
			},
			signal: options.signal
		});
	};
	let response = await connect();
	if (response.status === 401) {
		await refresh(true);
		response = await connect();
	}
	if (!response.ok || !response.body) {
		throw new Error(`assistant stream failed (${response.status})`);
	}
	options.onOpen?.();
	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let buffer = '';
	for (;;) {
		const { value, done } = await reader.read();
		if (done) return;
		buffer += decoder.decode(value, { stream: true });
		let cut = buffer.search(/\r?\n\r?\n/);
		while (cut !== -1) {
			const separator = buffer.slice(cut).match(/^\r?\n\r?\n/)?.[0] ?? '\n\n';
			const raw = buffer.slice(0, cut);
			buffer = buffer.slice(cut + separator.length);
			const frame = parseFrame(raw);
			if (frame) options.onEvent(frame);
			cut = buffer.search(/\r?\n\r?\n/);
		}
	}
}
