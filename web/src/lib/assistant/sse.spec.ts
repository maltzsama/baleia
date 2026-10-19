import { describe, expect, it } from 'vitest';
import { parseFrame, parseSse } from './sse';

describe('assistant SSE parser', () => {
	it('joins multi-line data and tolerates CRLF', () => {
		const frames = parseSse(
			'event: message\r\ndata: {"a":1}\r\ndata: {"b":2}\r\n\r\n: ping\n\ndata: hi\n\n'
		);
		expect(frames).toHaveLength(2);
		expect(frames[0]).toEqual({
			event: 'message',
			data: '{"a":1}\n{"b":2}',
			id: undefined
		});
		expect(frames[1]).toEqual({ event: 'message', data: 'hi', id: undefined });
	});

	it('keeps the id and ignores comment-only frames', () => {
		const frame = parseFrame('id: 7\nevent: delta\ndata: {"text":"hi"}');
		expect(frame).toEqual({ event: 'delta', data: '{"text":"hi"}', id: '7' });
		expect(parseFrame(': ping')).toBeNull();
	});
});
