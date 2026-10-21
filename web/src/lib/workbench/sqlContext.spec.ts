import { describe, expect, it } from 'vitest';
import { sqlContextAt } from './sqlContext';

// `|` marks the cursor.
function at(text: string) {
	const pos = text.indexOf('|');
	return sqlContextAt(text.slice(0, pos) + text.slice(pos + 1), pos);
}

describe('sqlContextAt', () => {
	it('expects a statement keyword at the start', () => {
		expect(at('s|')).toMatchObject({ expect: 'statement', prefix: 's', from: 0 });
		expect(at('SELECT 1; |')).toMatchObject({ expect: 'statement', prefix: '' });
	});

	it('expects columns in the select list', () => {
		expect(at('SELECT |')).toMatchObject({ expect: 'column', prefix: '' });
		expect(at('SELECT a, cou|')).toMatchObject({ expect: 'column', prefix: 'cou' });
	});

	it('expects a table right after FROM, JOIN and a comma', () => {
		expect(at('SELECT * FROM |')).toMatchObject({ expect: 'table', prefix: '' });
		expect(at('SELECT * FROM nat|')).toMatchObject({ expect: 'table', prefix: 'nat', from: 14 });
		expect(at('SELECT * FROM a JOIN |')).toMatchObject({ expect: 'table' });
		expect(at('SELECT * FROM a x, |')).toMatchObject({ expect: 'table' });
	});

	it('reads the schema before a table', () => {
		expect(at('SELECT * FROM tiny.|')).toMatchObject({
			expect: 'table',
			qualifier: ['tiny'],
			prefix: ''
		});
		expect(at('SELECT * FROM tpch.tiny.na|')).toMatchObject({
			expect: 'table',
			qualifier: ['tpch', 'tiny'],
			prefix: 'na'
		});
	});

	it('expects a clause keyword after a table and its alias', () => {
		expect(at('SELECT * FROM nation n W|')).toMatchObject({ expect: 'keyword', prefix: 'W' });
		expect(at('SELECT * FROM nation |')).toMatchObject({ expect: 'keyword' });
	});

	it('resolves an alias after the dot and while typing a column', () => {
		const dot = at('SELECT * FROM tiny.nation n WHERE n.|');
		expect(dot).toMatchObject({ expect: 'column', qualifier: ['n'], prefix: '' });
		expect(dot?.tables).toEqual([{ path: ['tiny', 'nation'], alias: 'n' }]);
		expect(at('SELECT * FROM tiny.nation n WHERE n.na|')).toMatchObject({
			expect: 'column',
			qualifier: ['n'],
			prefix: 'na'
		});
	});

	it('expects columns in WHERE, ON, GROUP BY, ORDER BY and HAVING', () => {
		for (const sql of [
			'SELECT * FROM nation WHERE nam|',
			'SELECT * FROM a JOIN b ON a.x = |',
			'SELECT x FROM a GROUP BY |',
			'SELECT x FROM a ORDER BY |',
			'SELECT x FROM a GROUP BY x HAVING |',
			'SELECT * FROM a WHERE x = 1 AND |'
		]) {
			expect(at(sql)?.expect, sql).toBe('column');
		}
	});

	it('collects the tables of the statement under the cursor only', () => {
		const ctx = at(
			'SELECT 1 FROM other;\nSELECT | FROM lake.sales.orders AS o JOIN customers c ON o.id = c.id, tiny.region;\nSELECT 2 FROM later'
		);
		expect(ctx?.tables).toEqual([
			{ path: ['lake', 'sales', 'orders'], alias: 'o' },
			{ path: ['customers'], alias: 'c' },
			{ path: ['tiny', 'region'], alias: 'region' }
		]);
	});

	it('reads quoted tables and derived tables', () => {
		expect(at('SELECT | FROM "My Table" t')?.tables).toEqual([{ path: ['My Table'], alias: 't' }]);
		expect(at('SELECT | FROM (SELECT * FROM inner_t) d')?.tables).toEqual([
			{ path: ['inner_t'], alias: 'inner_t' },
			{ path: [], alias: 'd' }
		]);
	});

	it('tracks the clause per parenthesis depth', () => {
		expect(at('SELECT * FROM (SELECT |')).toMatchObject({ expect: 'column' });
		expect(at('SELECT * FROM (|')).toMatchObject({ expect: 'statement' });
		expect(at('SELECT count(*) FROM t WHERE x IN (SELECT y FROM |')).toMatchObject({
			expect: 'table'
		});
		expect(at('SELECT * FROM t WHERE f(a) = |')).toMatchObject({ expect: 'column' });
	});

	it('keeps the column context inside function calls and expressions', () => {
		expect(at('SELECT count(|')).toMatchObject({ expect: 'column', prefix: '' });
		expect(at('SELECT coalesce(a, na|')).toMatchObject({ expect: 'column', prefix: 'na' });
		expect(at('SELECT * FROM t WHERE (a = 1 OR |')).toMatchObject({ expect: 'column' });
		expect(at('SELECT * FROM t WHERE x IN (SELECT |')).toMatchObject({ expect: 'column' });
		expect(at('SELECT * FROM t WHERE x IN (|')).toMatchObject({ expect: 'column' });
	});

	it('reads doubled quotes inside quoted identifiers', () => {
		expect(at('SELECT | FROM "a""b" t')?.tables).toEqual([{ path: ['a"b'], alias: 't' }]);
		expect(at('SELECT | FROM `a``b` t')?.tables).toEqual([{ path: ['a`b'], alias: 't' }]);
		expect(at('SELECT | FROM [a]]b] t')?.tables).toEqual([{ path: ['a]b'], alias: 't' }]);
		expect(at('SELECT "a""b|')).toBeNull();
		expect(at('SELECT "a""b" |')).toMatchObject({ expect: 'column' });
	});

	it('suggests nothing inside strings, comments and quoted identifiers', () => {
		expect(at("SELECT 'abc|")).toBeNull();
		expect(at("SELECT 'it''s |'")).toBeNull();
		expect(at('SELECT 1 -- note|')).toBeNull();
		expect(at('SELECT 1 /* x | */')).toBeNull();
		expect(at('SELECT "Col|')).toBeNull();
		expect(at("SELECT 'a' |")).toMatchObject({ expect: 'column' });
		expect(at('-- note\nSEL|')).toMatchObject({ expect: 'statement', prefix: 'SEL' });
	});

	it('does not complete inside a number', () => {
		expect(at('SELECT 12|')).toBeNull();
	});
});
