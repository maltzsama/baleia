import { describe, expect, it } from 'vitest';
import { formatBytes } from './size';

describe('formatBytes', () => {
	it('uses binary units', () => {
		expect(formatBytes(0)).toBe('0 B');
		expect(formatBytes(469)).toBe('469 B');
		expect(formatBytes(1536)).toBe('1.5 KiB');
		expect(formatBytes(1572864)).toBe('1.5 MiB');
		expect(formatBytes(200 * 1024 ** 3)).toBe('200 GiB');
	});

	it('answers a dash for a value that is not a size', () => {
		expect(formatBytes(-1)).toBe('—');
		expect(formatBytes(Number.NaN)).toBe('—');
	});
});
