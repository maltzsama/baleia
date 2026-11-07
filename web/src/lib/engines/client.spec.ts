import { describe, expect, it } from 'vitest';
import { PostgreSQL, StandardSQL } from '@maltzsama/lang-sql';
import { codemirrorDialect } from './client';

describe('codemirrorDialect', () => {
	it('maps the adapter base to a CodeMirror dialect', () => {
		expect(codemirrorDialect('PostgreSQL')).toBe(PostgreSQL);
		expect(codemirrorDialect('StandardSQL')).toBe(StandardSQL);
	});

	it('falls back to the standard dialect', () => {
		expect(codemirrorDialect(undefined)).toBe(StandardSQL);
		expect(codemirrorDialect('unknown')).toBe(StandardSQL);
	});
});
