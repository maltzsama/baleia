import { describe, expect, it } from 'vitest';
import { engineHealth, HEALTH_THRESHOLDS, memoryUsedPercent } from './health';

const base = {
	blocked_queries: 0,
	queued_queries: 0,
	total_memory_bytes: 100,
	free_memory_bytes: 50
};

describe('engineHealth', () => {
	it('is ok below the thresholds', () => {
		expect(engineHealth(base)).toBe('ok');
	});

	it('degrades on a queue at the threshold', () => {
		expect(engineHealth({ ...base, queued_queries: HEALTH_THRESHOLDS.queued })).toBe('degraded');
	});

	it('degrades on high memory', () => {
		expect(engineHealth({ ...base, free_memory_bytes: 20 })).toBe('degraded');
	});

	it('is critical with a blocked query', () => {
		expect(engineHealth({ ...base, blocked_queries: 1 })).toBe('critical');
	});

	it('is critical on nearly full memory', () => {
		expect(engineHealth({ ...base, free_memory_bytes: 5 })).toBe('critical');
	});

	it('ignores memory the engine did not report', () => {
		expect(engineHealth({ ...base, total_memory_bytes: null, free_memory_bytes: null })).toBe('ok');
	});

	it('treats a missing snapshot as degraded, never ok', () => {
		expect(engineHealth(null)).toBe('degraded');
	});
});

describe('memoryUsedPercent', () => {
	it('reads the free share', () => {
		expect(memoryUsedPercent(base)).toBe(50);
	});

	it('is null without a total', () => {
		expect(memoryUsedPercent({ ...base, total_memory_bytes: null })).toBeNull();
	});
});
