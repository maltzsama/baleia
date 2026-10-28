import { describe, expect, it } from 'vitest';
import { applySelection } from './selectionUrl';

describe('applySelection', () => {
	it('writes and overwrites selection parameters', () => {
		const url = new URL('http://localhost/sql?ws=ws-1&catalog=old');
		const next = applySelection(url, {
			catalog: 'lake',
			ns: ['sales', '2026'],
			table: 'orders',
			tab: 'result'
		});
		expect(next).toBe('/sql?ws=ws-1&catalog=lake&ns=sales&ns=2026&table=orders&tab=result');
	});

	it('removes parameters set to an empty value', () => {
		const url = new URL('http://localhost/data?ws=ws-1&catalog=lake&table=orders');
		expect(applySelection(url, { catalog: '', table: '', ns: [] })).toBe('/data?ws=ws-1');
	});

	it('round-trips an existing selection', () => {
		const original = 'http://localhost/data?ws=ws-1&catalog=lake&ns=sales&table=orders';
		const next = applySelection(new URL(original), {});
		expect(next).toBe('/data?ws=ws-1&catalog=lake&ns=sales&table=orders');
		expect(new URL(next, 'http://localhost').searchParams.getAll('ns')).toEqual(['sales']);
	});

	// #474: a column target rides along, encoded, and clears like the rest.
	it('sets and clears the column target', () => {
		const url = new URL('http://localhost/data?catalog=lake&table=orders');
		const next = applySelection(url, { column: 'order note/é' });
		expect(new URL(next, 'http://localhost').searchParams.get('column')).toBe('order note/é');
		expect(applySelection(new URL(next, 'http://localhost'), { column: '' })).toBe(
			'/data?catalog=lake&table=orders'
		);
	});
});
