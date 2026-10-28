import { describe, expect, it } from 'vitest';
import { eachLimited } from './limit';

describe('eachLimited', () => {
	it('runs every item with at most `limit` in flight, and survives failures', async () => {
		let running = 0;
		let peak = 0;
		const done: number[] = [];
		await eachLimited([1, 2, 3, 4, 5, 6, 7], 3, async (item) => {
			running++;
			peak = Math.max(peak, running);
			await new Promise((resolve) => setTimeout(resolve, 5));
			running--;
			if (item === 4) throw new Error('boom');
			done.push(item);
		});
		expect(peak).toBe(3);
		expect(done.sort()).toEqual([1, 2, 3, 5, 6, 7]);
	});
});
