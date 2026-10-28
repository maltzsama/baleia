import { afterEach, describe, expect, it } from 'vitest';
import { setAppLocale } from './locale.svelte';
import { plural } from './plural';

const one = ({ count }: { count: number | string }) => `${count} row`;
const other = ({ count }: { count: number | string }) => `${count} rows`;

describe('plural', () => {
	afterEach(() => setAppLocale('en'));

	it('follows the active locale: en has one only for 1, pt-BR also for 0', () => {
		expect([0, 1, 2].map((n) => plural(n, one, other))).toEqual(['0 rows', '1 row', '2 rows']);
		setAppLocale('pt-BR');
		expect([0, 1, 2].map((n) => plural(n, one, other))).toEqual(['0 row', '1 row', '2 rows']);
	});

	it('formats the count for the locale', () => {
		setAppLocale('pt-BR');
		expect(plural(12345, one, other)).toBe('12.345 rows');
	});
});
