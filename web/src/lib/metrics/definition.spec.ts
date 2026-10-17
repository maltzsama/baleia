import { describe, expect, it } from 'vitest';
import { describeDefinition, slugify } from './definition';

describe('metric definition in words', () => {
	it('describes an aggregation, filters and time grains', () => {
		const text = describeDefinition(
			{
				type: 'simple',
				aggregation: 'sum',
				column: 'total_price',
				filters: [{ column: 'status', op: 'in', values: ['paid', 'shipped'] }],
				time_column: 'order_date',
				time_grains: ['day', 'month']
			},
			{ catalog: 'lakehouse', namespace: ['sales'], table: 'orders' }
		);
		expect(text).toContain('Sum of `total_price` from lakehouse · sales · orders');
		expect(text).toContain('`status` is one of paid, shipped');
		expect(text).toContain('time column `order_date` (day, month)');
	});

	it('says count of all rows and renders null checks', () => {
		const text = describeDefinition(
			{ type: 'simple', aggregation: 'count', filters: [{ column: 'email', op: 'is_null' }] },
			{ catalog: 'c', namespace: [], table: 't' }
		);
		expect(text).toContain('Count of all rows');
		expect(text).toContain('`email` is null');
	});

	it('marks custom SQL as dialect-only', () => {
		const text = describeDefinition(
			{ type: 'simple', custom_sql: 'sum(x)' },
			{ catalog: 'c', namespace: ['n'], table: 't' },
			{ dialect: 'trino' }
		);
		expect(text).toContain('Custom expression (`trino` only)');
	});

	it('slugifies labels safely', () => {
		expect(slugify('Order count')).toBe('order_count');
		expect(slugify('123 total')).toBe('m_123_total');
	});
});
