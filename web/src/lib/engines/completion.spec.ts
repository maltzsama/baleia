import { beforeEach, describe, expect, it } from 'vitest';
import { clearMetadataCache } from '$lib/metadata/client';
import { http, server } from '../../test/server';
import { makeSearchResult, makeTableDetail } from '../../test/factories';
import { clearCompletionCache, resolveAliasColumns, tableForAlias } from './completion';

const WS = '00000000-0000-0000-0000-000000000001';

describe('tableForAlias', () => {
	it('resolves a plain alias', () => {
		expect(tableForAlias('SELECT * FROM orders o WHERE o.', 'o')).toBe('orders');
	});

	it('drops the catalog and schema from a qualified table', () => {
		expect(tableForAlias('SELECT * FROM lake.sales.orders AS o', 'o')).toBe('orders');
	});

	it('reads a quoted table and a JOIN alias', () => {
		expect(tableForAlias('SELECT * FROM "MyTable" t', 't')).toBe('MyTable');
		expect(tableForAlias('SELECT * FROM orders o JOIN customers AS c ON o.id = c.id', 'c')).toBe(
			'customers'
		);
	});

	it('falls back to the alias when no FROM clause matches', () => {
		expect(tableForAlias('SELECT 1', 'x')).toBe('x');
	});
});

describe('resolveAliasColumns', () => {
	beforeEach(() => {
		clearMetadataCache();
		clearCompletionCache();
	});

	it('returns the columns of the table an alias refers to', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/search', ({ response }) =>
				response(200).json([makeSearchResult({ kind: 'table', table: 'orders' })])
			),
			http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) =>
				response(200).json(makeTableDetail())
			)
		);

		const columns = await resolveAliasColumns(
			WS,
			'SELECT * FROM lake.sales.orders o WHERE o.',
			'o'
		);

		expect(columns.map((column) => column.name)).toEqual(['id', 'order_note']);
	});

	it('caches the columns per table', async () => {
		let tableCalls = 0;
		server.use(
			http.get('/workspaces/{id}/metadata/search', ({ response }) =>
				response(200).json([makeSearchResult({ kind: 'table', table: 'orders' })])
			),
			http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) => {
				tableCalls += 1;
				return response(200).json(makeTableDetail());
			})
		);

		await resolveAliasColumns(WS, 'SELECT * FROM orders o', 'o');
		await resolveAliasColumns(WS, 'SELECT * FROM orders o WHERE o.', 'o');

		expect(tableCalls).toBe(1);
	});
});
