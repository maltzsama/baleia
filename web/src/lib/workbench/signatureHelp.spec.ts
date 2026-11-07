import { describe, expect, it } from 'vitest';
import { callAt } from './signatureHelp';

// `|` marks the cursor.
const at = (text: string) => {
	const pos = text.indexOf('|');
	return callAt(text.slice(0, pos) + text.slice(pos + 1), pos);
};

describe('callAt (#790)', () => {
	it('reads the function name and the argument the cursor is in', () => {
		expect(at('SELECT count(|')).toEqual({ name: 'count', argIndex: 0 });
		expect(at('SELECT coalesce(a, |')).toEqual({ name: 'coalesce', argIndex: 1 });
		expect(at('SELECT coalesce(a, b, |')).toEqual({ name: 'coalesce', argIndex: 2 });
	});

	it('counts only top-level commas', () => {
		expect(at('SELECT f(g(1, 2), |')).toEqual({ name: 'f', argIndex: 1 });
	});

	it('reads the innermost call', () => {
		expect(at('SELECT f(g(|')).toEqual({ name: 'g', argIndex: 0 });
	});

	it('returns null outside a call', () => {
		expect(at('SELECT count(*) FROM t|')).toBeNull();
		expect(at('SELECT 1|')).toBeNull();
		expect(at('SELECT count|')).toBeNull();
	});
});
