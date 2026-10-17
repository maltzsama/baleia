import { describe, expect, it } from 'vitest';
import { poll } from './poll';

describe('poll', () => {
	it('resolves when the terminal predicate matches', async () => {
		let calls = 0;
		const { done } = poll(async () => ++calls, {
			until: (value) => value >= 3,
			intervals: [1, 1],
			maxDurationMs: 1000
		});
		await expect(done).resolves.toBe(3);
	});

	it('stops and aborts the in-flight request', async () => {
		let signal: AbortSignal | undefined;
		const { stop, done } = poll(
			async (abortSignal) => {
				signal = abortSignal;
				return 0;
			},
			{ until: () => false, intervals: [1000], maxDurationMs: 1000 }
		);
		await Promise.resolve();
		stop();
		expect(signal?.aborted).toBe(true);
		await expect(done).resolves.toBe('stopped');
	});

	it('times out after maxDurationMs', async () => {
		const { done } = poll(async () => 0, {
			until: () => false,
			intervals: [1],
			maxDurationMs: 5
		});
		await expect(done).resolves.toBe('timeout');
	});
});
