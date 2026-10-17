import { describe, expect, it } from 'vitest';
import { historyQuery, parseHistoryFilters } from './filters';

describe('history filters', () => {
	it('parses the filters from the URL', () => {
		const url = new URL(
			'http://localhost/history?status=failed&q=orders&from=2026-10-01T10%3A00&to=2026-10-02T10%3A00'
		);
		expect(parseHistoryFilters(url)).toEqual({
			status: 'failed',
			q: 'orders',
			from: '2026-10-01T10:00',
			to: '2026-10-02T10:00'
		});
	});

	it('defaults every filter to empty', () => {
		expect(parseHistoryFilters(new URL('http://localhost/history'))).toEqual({
			status: '',
			q: '',
			from: '',
			to: ''
		});
	});

	it('builds the API query from the filters and cursor', () => {
		const query = historyQuery(
			{ status: 'failed', q: 'orders', from: '2026-10-01T10:00', to: '' },
			'cursor-1'
		);
		expect(query.status).toBe('failed');
		expect(query.q).toBe('orders');
		expect(query.from).toBe(new Date('2026-10-01T10:00').toISOString());
		expect(query.to).toBeUndefined();
		expect(query.cursor).toBe('cursor-1');
		expect(query.limit).toBe(50);
	});

	it('omits empty filters', () => {
		const query = historyQuery({ status: '', q: '', from: '', to: '' });
		expect(query).toEqual({ limit: 50 });
	});
});
