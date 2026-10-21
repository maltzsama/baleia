import { beforeEach, describe, expect, it } from 'vitest';
import { CompletionContext, type Completion } from '@codemirror/autocomplete';
import { EditorState } from '@codemirror/state';
import { MySQL, StandardSQL, sql } from '@codemirror/lang-sql';
import { clearMetadataCache } from '$lib/metadata/client';
import { http, server } from '../../test/server';
import { makeSearchResult, makeTableDetail } from '../../test/factories';
import { identifier, sqlCompletionSource, type CompletionScope } from './sqlCompletion';

const WS = '00000000-0000-0000-0000-000000000001';
const CATALOG = '00000000-0000-0000-0000-0000000000c1';
const IDS = {
	tinyNation: '00000000-0000-0000-0000-0000000000a1',
	tinyRegion: '00000000-0000-0000-0000-0000000000a2',
	sf1Nation: '00000000-0000-0000-0000-0000000000a3',
	tinyPart: '00000000-0000-0000-0000-0000000000a4'
};

const column = (ordinal: number, name: string, type: string) => ({
	ordinal,
	name,
	native_type: type,
	type_system: 'trino',
	display_type: type
});

const details = {
	[IDS.tinyNation]: makeTableDetail({
		id: IDS.tinyNation,
		namespace: ['tiny'],
		name: 'nation',
		columns: [
			column(1, 'nationkey', 'bigint'),
			column(2, 'name', 'varchar(25)'),
			column(3, 'regionkey', 'bigint')
		]
	}),
	[IDS.tinyRegion]: makeTableDetail({
		id: IDS.tinyRegion,
		namespace: ['tiny'],
		name: 'region',
		columns: [column(1, 'regionkey', 'bigint'), column(2, 'name', 'varchar(25)')]
	}),
	[IDS.tinyPart]: makeTableDetail({
		id: IDS.tinyPart,
		namespace: ['tiny'],
		name: 'part',
		columns: [column(1, 'partkey', 'bigint'), column(2, 'name', 'varchar(55)')]
	})
};

const tablesByNs: Record<string, { id: string; name: string; kind: 'table' }[]> = {
	tiny: [
		{ id: IDS.tinyNation, name: 'nation', kind: 'table' },
		{ id: IDS.tinyPart, name: 'part', kind: 'table' },
		{ id: IDS.tinyRegion, name: 'region', kind: 'table' }
	],
	sf1: [{ id: IDS.sf1Nation, name: 'nation', kind: 'table' }]
};

const scope = (overrides: Partial<CompletionScope> = {}): CompletionScope => ({
	workspaceId: WS,
	catalogId: CATALOG,
	bindingId: '00000000-0000-0000-0000-0000000000b1',
	engineCatalog: 'tpch',
	namespaceDepth: 1,
	schema: 'tiny',
	keywords: [],
	functions: ['count', 'count_if', 'ST_Area', 'abs'],
	cmDialect: StandardSQL,
	upperCase: true,
	...overrides
});

let searches: { q: string; catalogId: string | null }[] = [];

beforeEach(() => {
	clearMetadataCache();
	searches = [];
	server.use(
		http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces', ({ response }) =>
			response(200).json({
				freshness: { metadata_synced_at: '2026-01-01T00:00:00Z' },
				items: [{ path: ['sf1'] }, { path: ['tiny'] }]
			})
		),
		http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/tables', ({ request, response }) => {
			const ns = new URL(request.url).searchParams.get('ns') ?? '';
			return response(200).json({
				freshness: { metadata_synced_at: '2026-01-01T00:00:00Z' },
				items: tablesByNs[ns] ?? [],
				next_cursor: null
			});
		}),
		http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ params, response }) =>
			response(200).json(details[params.table_id as string] ?? makeTableDetail())
		),
		http.get('/workspaces/{id}/metadata/search', ({ request, response }) => {
			const url = new URL(request.url);
			searches.push({
				q: url.searchParams.get('q') ?? '',
				catalogId: url.searchParams.get('catalog_id')
			});
			return response(200).json([
				makeSearchResult({ table_id: IDS.sf1Nation, namespace: ['sf1'], table: 'nation' }),
				makeSearchResult({ table_id: IDS.tinyNation, namespace: ['tiny'], table: 'nation' }),
				makeSearchResult({
					table_id: IDS.tinyRegion,
					namespace: ['tiny'],
					table: 'nationality_like'
				})
			]);
		})
	);
});

// `|` marks the cursor.
async function complete(
	text: string,
	options: { explicit?: boolean; scope?: CompletionScope } = {}
) {
	const pos = text.indexOf('|');
	const doc = text.slice(0, pos) + text.slice(pos + 1);
	const state = EditorState.create({ doc, extensions: [sql({ dialect: StandardSQL })] });
	const source = sqlCompletionSource(() => options.scope ?? scope());
	const result = await source(new CompletionContext(state, pos, options.explicit ?? false));
	return result;
}

const labels = (options: readonly Completion[] | undefined) =>
	(options ?? []).map((option) => `${option.label}${option.detail ? ` · ${option.detail}` : ''}`);

describe('sqlCompletionSource', () => {
	it('offers statement keywords at the start, not functions', async () => {
		const result = await complete('s|');
		const got = labels(result?.options);
		expect(got).toContain('SELECT');
		expect(got).toContain('SHOW');
		expect(got.some((label) => /^(SHA|SIN|SECOND)/i.test(label))).toBe(false);
	});

	it('offers the tab schema tables and the schemas after FROM', async () => {
		const result = await complete('SELECT * FROM |', { explicit: true });
		expect(labels(result?.options)).toEqual([
			'nation · tiny',
			'part · tiny',
			'region · tiny',
			'sf1 · schema',
			'tiny · schema'
		]);
	});

	it('ranks the tab schema first and qualifies tables of other schemas', async () => {
		const result = await complete('SELECT * FROM nat|');
		expect(labels(result?.options)).toEqual(['nation · tiny', 'nation · sf1']);
		expect(result?.options[0].apply).toBe('nation');
		expect(result?.options[1].apply).toBe('sf1.nation');
		expect(searches).toEqual([{ q: 'nat', catalogId: CATALOG }]);
		expect(result?.from).toBe(14);
	});

	it('lists the tables of a schema after its name', async () => {
		const result = await complete('SELECT * FROM sf1.|');
		expect(labels(result?.options)).toEqual(['nation · sf1']);
		expect(result?.options[0].apply).toBe('nation');
	});

	it('lists schemas after the engine catalog name', async () => {
		const result = await complete('SELECT * FROM tpch.|');
		expect(labels(result?.options)).toEqual(['sf1 · schema', 'tiny · schema']);
	});

	it('offers only the aliased table columns after the dot and while typing', async () => {
		const dot = await complete('SELECT * FROM tiny.nation n WHERE n.|');
		expect(labels(dot?.options)).toEqual([
			'nationkey · bigint',
			'name · varchar(25)',
			'regionkey · bigint'
		]);
		const typing = await complete('SELECT * FROM tiny.nation n WHERE n.na|');
		expect(labels(typing?.options)).toEqual(['nationkey · bigint', 'name · varchar(25)']);
		expect(typing?.from).toBe('SELECT * FROM tiny.nation n WHERE n.'.length);
	});

	it('offers nothing for an unknown alias', async () => {
		expect(await complete('SELECT * FROM tiny.nation n WHERE x.|')).toBeNull();
	});

	it('puts columns of the statement tables before functions and keywords', async () => {
		// Only nation's `name`: part and region are not in the statement.
		const result = await complete('SELECT * FROM nation WHERE nam|');
		expect(labels(result?.options)).toEqual(['name · varchar(25)', 'NAMES']);
		const many = await complete(
			'SELECT | FROM nation n JOIN region r ON n.regionkey = r.regionkey',
			{
				explicit: true
			}
		);
		const got = labels(many?.options);
		expect(got.slice(0, 3)).toEqual([
			'nationkey · bigint',
			'name · varchar(25)',
			'regionkey · bigint'
		]);
		expect(got.indexOf('ABS · function')).toBeGreaterThan(2);
		expect(got.indexOf('ABS · function')).toBeLessThan(got.indexOf('AND'));
	});

	it('matches by prefix, never by similarity', async () => {
		const result = await complete('SELECT cou| FROM nation');
		expect(labels(result?.options)).toEqual(['COUNT · function', 'COUNT_IF · function']);
	});

	it('suggests nothing inside a string or a comment', async () => {
		expect(await complete("SELECT * FROM nation WHERE name = 'na|")).toBeNull();
		expect(await complete('SELECT 1 -- na|')).toBeNull();
	});

	it('uses the dialect keyword list when there is one', async () => {
		const result = await complete('SELECT * FROM nation |', {
			explicit: true,
			scope: scope({ keywords: ['where', 'window'] })
		});
		expect(labels(result?.options)).toEqual(['WHERE', 'WINDOW']);
	});
});

describe('identifier', () => {
	it('quotes only names that need it, with the dialect quote', () => {
		expect(identifier('nation', StandardSQL)).toBe('nation');
		expect(identifier('My Table', StandardSQL)).toBe('"My Table"');
		expect(identifier('order', StandardSQL)).toBe('"order"');
		expect(identifier('a"b', StandardSQL)).toBe('"a""b"');
		expect(identifier('My Table', MySQL)).toBe('`My Table`');
	});
});
