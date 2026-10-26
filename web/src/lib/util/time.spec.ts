import { describe, expect, it } from 'vitest';
import { formatRelativeTime } from './time';

const NOW = Date.parse('2026-01-01T12:00:00Z');

describe('formatRelativeTime', () => {
	it('follows the locale (#412)', () => {
		expect(formatRelativeTime(NOW - 10_000, NOW, 'en')).toBe('just now');
		expect(formatRelativeTime(NOW - 10_000, NOW, 'pt-BR')).toBe('agora mesmo');
		expect(formatRelativeTime(NOW - 5 * 60_000, NOW, 'en')).toBe('5 minutes ago');
		expect(formatRelativeTime(NOW - 5 * 60_000, NOW, 'pt-BR')).toBe('há 5 minutos');
	});

	it('defaults to the active locale', () => {
		expect(formatRelativeTime(NOW - 2 * 3_600_000, NOW)).toBe('2 hours ago');
	});

	it('answers a dash for an invalid date', () => {
		expect(formatRelativeTime('not a date', NOW, 'en')).toBe('—');
	});
});
