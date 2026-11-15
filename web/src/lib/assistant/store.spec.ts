import { describe, expect, it } from 'vitest';
import type { SseFrame } from './sse';
import { AssistantStore } from './store.svelte';

function frame(event: string, data: unknown): SseFrame {
	return { event, data: JSON.stringify(data) };
}

function receive(store: AssistantStore, value: SseFrame) {
	(store as unknown as { onFrame: (frame: SseFrame) => void }).onFrame(value);
}

describe('assistant store stream recovery', () => {
	it('applies the persisted state snapshot after reconnect', () => {
		const store = new AssistantStore();
		receive(store, frame('state', { state: 'running', last_seq: 4 }));
		expect(store.state).toBe('running');
	});

	it('ignores duplicate messages and tolerates malformed deltas', () => {
		const store = new AssistantStore();
		const message = { seq: 2, role: 'assistant', content: 'ok' };
		receive(store, frame('message', message));
		receive(store, frame('message', message));
		expect(store.messages).toHaveLength(1);
		receive(store, { event: 'delta', data: '{' });
		expect(store.streamingText).toBe('');
	});

	it('appends valid deltas without making them authoritative', () => {
		const store = new AssistantStore();
		receive(store, frame('delta', { text: 'partial' }));
		expect(store.streamingText).toBe('partial');
		expect(store.messages).toHaveLength(0);
	});
});
