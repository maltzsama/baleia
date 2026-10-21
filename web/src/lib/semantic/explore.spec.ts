import { describe, expect, it } from 'vitest';
import {
	canonicalQuery,
	decodeShare,
	definitionOf,
	dimensionsFromColumns,
	encodeShare,
	exploreLinkOf,
	fieldTree,
	migratedModelOf,
	filterGroups,
	offendingFields,
	operatorsFor,
	parseFilterValues,
	withExploreComment,
	type FieldNode,
	type SemanticModel
} from './explore';

function model(slug: string, definition: Record<string, unknown>): SemanticModel {
	return {
		id: slug,
		workspace_id: 'w1',
		slug,
		label: slug.toUpperCase(),
		catalog_id: 'c1',
		certified: false,
		definition,
		format_version: 1,
		updated_at: '2026-01-01T00:00:00Z',
		version: 1
	};
}

const orders = model('orders', {
	source: { kind: 'table', table: 'orders' },
	dimensions: [
		{ name: 'status', type: 'string' },
		{ name: 'internal_id', type: 'int64', hidden: true },
		{ name: 'created_at', type: 'timestamp', grains: ['day', 'month'] }
	],
	metrics: [{ name: 'revenue', aggregation: 'sum', dimension: 'amount' }],
	calculated_fields: [{ name: 'avg_ticket', sql: 'revenue / 2' }],
	joins: [{ to: 'customers', relationship: 'many_to_one', on: [{ from: 'cid', to: 'id' }] }]
});
const customers = model('customers', {
	source: { kind: 'table', table: 'customers' },
	dimensions: [{ name: 'country', label: 'Country', type: 'string' }],
	metrics: [{ name: 'customer_count', aggregation: 'count' }]
});

describe('share link', () => {
	it('round-trips a query through base64url canonical JSON', () => {
		const query = {
			model: 'orders',
			dimensions: ['status'],
			metrics: ['revenue'],
			filters: [{ field: 'status', op: 'in' as const, values: ['paid', 'ação'] }],
			sorts: [{ field: 'revenue', desc: true }],
			limit: 100,
			time_grain: 'month'
		};
		const encoded = encodeShare(query);
		expect(encoded).toMatch(/^[A-Za-z0-9_-]+$/);
		expect(decodeShare(encoded)).toEqual(query);
	});

	it('matches the Go canonical field order and omits empty fields', () => {
		const raw = JSON.stringify(
			canonicalQuery({
				model: 'orders',
				time_grain: 'day',
				limit: 5,
				metrics: ['revenue'],
				dimensions: [],
				sorts: [{ field: 'revenue', desc: false }]
			})
		);
		expect(raw).toBe(
			'{"model":"orders","metrics":["revenue"],"sorts":[{"field":"revenue"}],"limit":5,"time_grain":"day"}'
		);
	});

	it('rejects garbage, unknown fields and raw SQL', () => {
		expect(decodeShare(null)).toBeNull();
		expect(decodeShare('not base64 !!')).toBeNull();
		const withSql = btoa(JSON.stringify({ model: 'orders', sql: 'select 1' }));
		expect(decodeShare(withSql)).toBeNull();
		expect(decodeShare(btoa(JSON.stringify({ model: 3 })))).toBeNull();
		expect(decodeShare(btoa(JSON.stringify({ model: 'a', limit: -1 })))).toBeNull();
	});
});

describe('field tree', () => {
	it('lists the base model first, hides hidden fields, then joined models under the join name', () => {
		const groups = fieldTree([orders, customers], 'orders');
		expect(groups.map((g) => [g.model, g.join])).toEqual([
			['orders', undefined],
			['customers', 'customers']
		]);
		expect(groups[0].dimensions.map((d) => d.name)).toEqual(['status', 'created_at']);
		expect(groups[0].metrics.map((f) => [f.name, f.kind])).toEqual([
			['revenue', 'metric'],
			['avg_ticket', 'calculated']
		]);
		expect(groups[1].dimensions[0].label).toBe('Country');
	});

	it('marks a name shared across the join tree as ambiguous', () => {
		const clash = model('customers', {
			source: { kind: 'table', table: 'customers' },
			dimensions: [{ name: 'status', type: 'string' }]
		});
		const groups = fieldTree([orders, clash], 'orders');
		expect(groups[0].dimensions.find((d) => d.name === 'status')?.ambiguous).toBe(true);
		expect(groups[1].dimensions[0].ambiguous).toBe(true);
		expect(groups[0].dimensions.find((d) => d.name === 'created_at')?.ambiguous).toBeUndefined();
	});

	it('returns nothing for an unknown base model and ignores missing join targets', () => {
		expect(fieldTree([orders], 'nope')).toEqual([]);
		expect(fieldTree([orders], 'orders')).toHaveLength(1);
	});

	it('searches by name or label and drops empty groups', () => {
		const groups = fieldTree([orders, customers], 'orders');
		expect(filterGroups(groups, 'countr').map((g) => g.model)).toEqual(['customers']);
		expect(filterGroups(groups, '')).toBe(groups);
	});
});

describe('filters', () => {
	const dim = (type: FieldNode['type']): FieldNode => ({
		name: 'x',
		label: 'x',
		kind: 'dimension',
		type,
		grains: [],
		model: 'm'
	});

	it('offers operators by logical type', () => {
		expect(operatorsFor(dim('boolean'))).not.toContain('>');
		expect(operatorsFor(dim('string'))).toContain('in');
		expect(operatorsFor(dim('string'))).not.toContain('>');
		expect(operatorsFor(dim('int64'))).toContain('>=');
		expect(operatorsFor({ ...dim('int64'), kind: 'metric' })).not.toContain('in');
	});

	it('types the values', () => {
		expect(parseFilterValues(dim('int64'), 'in', '1, 2 ,x')).toEqual([1, 2, 'x']);
		expect(parseFilterValues(dim('string'), '=', '42')).toEqual(['42']);
		expect(parseFilterValues(dim('boolean'), '=', 'true')).toEqual([true]);
		expect(parseFilterValues(dim('string'), 'is_null', 'ignored')).toEqual([]);
		expect(parseFilterValues({ ...dim('string'), kind: 'metric' }, '>', '10')).toEqual([10]);
	});
});

describe('compiler errors', () => {
	it('finds the selected fields a message quotes', () => {
		const message = 'invalid explore query: unknown dimension "regio"';
		expect(offendingFields(message, ['status', 'regio'])).toEqual(['regio']);
		expect(offendingFields('boom', ['status'])).toEqual([]);
	});
});

describe('saved query round trip', () => {
	it('keeps the explore link as the first comment line', () => {
		const query = { model: 'orders', metrics: ['revenue'] };
		const sql = withExploreComment('SELECT 1', query);
		expect(sql.endsWith('\nSELECT 1')).toBe(true);
		const link = exploreLinkOf(sql);
		expect(link).toBe(`/explore?q=${encodeShare(query)}`);
		expect(exploreLinkOf('SELECT 1')).toBeNull();
	});
});

describe('model helpers', () => {
	it('generates dimensions from columns, skipping unsupported types', () => {
		const dims = dimensionsFromColumns([
			{ name: 'id', logical_type: 'int64' },
			{ name: 'price', logical_type: 'decimal(10,2)' },
			{ name: 'payload', logical_type: 'json' },
			{ name: 'day', logical_type: 'date' }
		]);
		expect(dims.map((d) => [d.name, d.type])).toEqual([
			['id', 'int64'],
			['price', 'float64'],
			['day', 'date']
		]);
		expect(dims[2].grains).toContain('month');
		expect(dims[0].grains).toBeUndefined();
	});

	it('reads the stored definition', () => {
		expect(definitionOf(orders).source.table).toBe('orders');
	});
});

describe('v1 migration redirect', () => {
	const migrated = model('orders', {
		source: { kind: 'table', namespace: ['public'], table: 'orders' },
		metrics: [{ name: 'revenue', aggregation: 'count', migrated_from: 'm1' }]
	});
	const lookalike = model('orders_copy', {
		source: { kind: 'table', namespace: ['public'], table: 'orders' },
		metrics: [{ name: 'revenue', aggregation: 'count' }]
	});

	it('finds the model whose metric records the v1 metric as its source', () => {
		expect(
			migratedModelOf([customers, lookalike, migrated], { id: 'm1', catalog_id: 'c1' })?.slug
		).toBe('orders');
	});

	it('ignores a metric that only shares the slug, and other catalogs', () => {
		expect(migratedModelOf([lookalike], { id: 'm1', catalog_id: 'c1' })).toBeUndefined();
		expect(migratedModelOf([migrated], { id: 'm1', catalog_id: 'c2' })).toBeUndefined();
		expect(migratedModelOf([migrated], { id: 'm2', catalog_id: 'c1' })).toBeUndefined();
	});
});
