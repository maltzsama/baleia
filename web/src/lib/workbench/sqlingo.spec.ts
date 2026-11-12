// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { DEFAULT_TARGET_DIALECT, defaultTargetDialect, optimizeSQL, transpileSQL } from './sqlingo';

describe('transpileSQL', () => {
	it('lowers PostgreSQL ILIKE for Trino', async () => {
		const out = await transpileSQL(
			'postgresql',
			'trino',
			"SELECT id FROM users WHERE name ILIKE '%a%'"
		);
		expect(out).toContain('LOWER(name) LIKE LOWER');
	});

	it('keeps every statement', async () => {
		const out = await transpileSQL('postgresql', 'trino', 'SELECT 1; SELECT 2');
		expect(out.split(';').length).toBeGreaterThanOrEqual(2);
	});

	it('throws on an incomplete script', async () => {
		await expect(transpileSQL('postgresql', 'trino', 'SELECT * FROM')).rejects.toThrow();
	});
});

describe('optimizeSQL', () => {
	it('folds a constant predicate away', async () => {
		const out = await optimizeSQL(
			'postgresql',
			'SELECT * FROM (SELECT 1 AS a) t WHERE a = 1 AND 1 = 1'
		);
		expect(out).not.toContain('1 = 1');
	});

	it('throws on an incomplete script', async () => {
		await expect(optimizeSQL('postgresql', 'SELECT * FROM')).rejects.toThrow();
	});
});

describe('defaultTargetDialect', () => {
	it('is Trino, never Presto', () => {
		expect(DEFAULT_TARGET_DIALECT).toBe('trino');
		expect(defaultTargetDialect()).toBe('trino');
	});
});
