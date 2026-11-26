import { describe, expect, it } from 'vitest';
import { makeDataPolicy } from '../../test/factories';
import {
	combinedFilter,
	combineWrites,
	effectsFor,
	groupByTable,
	maskExpression,
	policyApplies,
	subjectFromKey,
	subjectKey
} from './policies';

const ana = '00000000-0000-0000-0000-0000000000a0';
const bo = '00000000-0000-0000-0000-0000000000b0';
const team = '00000000-0000-0000-0000-0000000000c0';

// #1146: the governance screens' rules, from ADR 0028's policy model.
describe('subject keys', () => {
	it.each([
		[{ user: ana }, `user:${ana}`],
		[{ team }, `team:${team}`],
		[{ group: 'data-eu' }, 'group:data-eu'],
		[{ service_account: bo }, `service_account:${bo}`],
		[{ everyone: true }, 'everyone']
	])('round-trips %j', (subject, key) => {
		expect(subjectKey(subject)).toBe(key);
		expect(subjectFromKey(key)).toEqual(subject);
	});

	it('rejects a malformed key', () => {
		expect(subjectFromKey('')).toBeNull();
		expect(subjectFromKey('user:')).toBeNull();
		expect(subjectFromKey('role:x')).toBeNull();
	});
});

describe('policyApplies', () => {
	it('matches a named subject, or a team the user belongs to', () => {
		const policy = makeDataPolicy({ subjects: [{ team }] });
		expect(policyApplies(policy, new Set([`user:${ana}`]))).toBe(false);
		expect(policyApplies(policy, new Set([`user:${ana}`, `team:${team}`]))).toBe(true);
	});

	it('matches everyone except the exceptions, through a team too', () => {
		const policy = makeDataPolicy({ subjects: [{ everyone: true, except: [{ team }] }] });
		expect(policyApplies(policy, new Set([`user:${bo}`]))).toBe(true);
		expect(policyApplies(policy, new Set([`user:${ana}`, `team:${team}`]))).toBe(false);
	});
});

describe('groupByTable', () => {
	it('sorts tables, and filters before masks', () => {
		const groups = groupByTable([
			makeDataPolicy({ id: '1', table: 'b', kind: 'column_mask', column: 'email' }),
			makeDataPolicy({ id: '2', table: 'b' }),
			makeDataPolicy({ id: '3', table: 'a' })
		]);
		expect(groups.map((g) => [g.label, g.policies.map((p) => p.id)])).toEqual([
			['sales.a', ['3']],
			['sales.b', ['2', '1']]
		]);
	});

	it('keeps dotted schema and table names apart', () => {
		const groups = groupByTable([
			makeDataPolicy({ id: '1', schema: 'a.b', table: 'c' }),
			makeDataPolicy({ id: '2', schema: 'a', table: 'b.c' })
		]);
		expect(groups).toHaveLength(2);
		expect(groups.map((g) => g.label)).toEqual(['a.b.c', 'a.b.c']);
		expect(groups.map((g) => g.policies.map((p) => p.id)).sort()).toEqual([['1'], ['2']]);
		expect(new Set(groups.map((g) => g.key)).size).toBe(2);
	});
});

describe('combineWrites', () => {
	it('takes the most restrictive effect of each write', () => {
		const filter = makeDataPolicy();
		const mask = makeDataPolicy({
			kind: 'column_mask',
			writes: { insert: 'refused', update: 'refused', delete: 'refused', merge: 'refused' }
		});
		expect(combineWrites([filter])).toEqual(filter.writes);
		expect(combineWrites([filter, mask])).toEqual(mask.writes);
		expect(combineWrites([])).toBeNull();
	});
});

describe('maskExpression', () => {
	it('fills each template', () => {
		expect(maskExpression('null_value', 'email', { type: 'varchar' })).toBe(
			'CAST(NULL AS VARCHAR)'
		);
		expect(maskExpression('constant', 'email', { value: "it's" })).toBe("'it''s'");
		expect(maskExpression('partial', 'phone', { keep: 4, from: 'end' })).toBe(
			'concat(\'***\', substr("phone", -4))'
		);
		expect(maskExpression('partial', 'phone', { keep: 2, from: 'start' })).toBe(
			'concat(substr("phone", 1, 2), \'***\')'
		);
		expect(maskExpression('sha256', 'Tax ID')).toBe('to_hex(sha256(to_utf8("Tax ID")))');
		// A reserved word is a valid column once quoted.
		expect(maskExpression('sha256', 'order')).toBe('to_hex(sha256(to_utf8("order")))');
	});

	it('never puts anything but a type name into CAST', () => {
		expect(maskExpression('null_value', 'c', { type: 'varchar) OR (1=1' })).toBe(
			'CAST(NULL AS VARCHAR)'
		);
		expect(maskExpression('null_value', 'c', { type: 'decimal(10, 2)' })).toBe(
			'CAST(NULL AS DECIMAL(10, 2))'
		);
	});
});

describe('effectsFor', () => {
	const refs = new Set([`user:${ana}`]);

	it('is default-deny once a table has a row filter', () => {
		const [effect] = effectsFor([makeDataPolicy({ subjects: [{ user: bo }] })], refs);
		expect(effect.noRows).toBe(true);
		expect(effect.permissive).toEqual([]);
	});

	it('combines permissive and restrictive filters that match', () => {
		const [effect] = effectsFor(
			[
				makeDataPolicy({ id: 'p', subjects: [{ user: ana }] }),
				makeDataPolicy({ id: 'r', mode: 'restrictive', subjects: [{ everyone: true }] }),
				makeDataPolicy({ id: 'off', state: 'disabled', subjects: [{ user: ana }] })
			],
			refs
		);
		expect(effect.noRows).toBe(false);
		expect(effect.permissive.map((p) => p.id)).toEqual(['p']);
		expect(effect.restrictive.map((p) => p.id)).toEqual(['r']);
	});

	it('applies the highest-priority matching mask of a column', () => {
		const mask = (id: string, priority: number) =>
			makeDataPolicy({
				id,
				kind: 'column_mask',
				column: 'email',
				priority,
				subjects: [{ everyone: true }]
			});
		const [effect] = effectsFor([mask('low', 1), mask('high', 9)], refs);
		expect(effect.filtered).toBe(false);
		expect(effect.masks[0].applied.id).toBe('high');
		expect(effect.masks[0].shadowed.map((p) => p.id)).toEqual(['low']);
	});

	it('leaves out tables where nothing applies', () => {
		const masked = makeDataPolicy({
			kind: 'column_mask',
			column: 'email',
			subjects: [{ user: bo }]
		});
		expect(effectsFor([masked], refs)).toEqual([]);
	});
});

describe('combinedFilter', () => {
	const p = (expression: string) => makeDataPolicy({ expression });

	it('narrows the whole permissive union with each restrictive filter', () => {
		expect(
			combinedFilter({ permissive: [p('a = 1'), p('b = 2')], restrictive: [p('c = 3')] })
		).toBe('((a = 1) OR (b = 2)) AND (c = 3)');
	});

	it('keeps a single filter as it was', () => {
		expect(combinedFilter({ permissive: [p('a = 1')], restrictive: [] })).toBe('(a = 1)');
		expect(combinedFilter({ permissive: [p('a = 1')], restrictive: [p('c = 3')] })).toBe(
			'(a = 1) AND (c = 3)'
		);
		expect(combinedFilter({ permissive: [], restrictive: [p('c = 3')] })).toBe('(c = 3)');
	});
});
