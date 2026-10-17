import { describe, expect, it } from 'vitest';
import { PostgreSQL, StandardSQL } from '@codemirror/lang-sql';
import { codemirrorDialect, formatterLanguage } from './client';

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

describe('formatterLanguage', () => {
	it('uses the dialect formatter id', () => {
		expect(formatterLanguage('trino')).toBe('trino');
		expect(formatterLanguage('postgresql')).toBe('postgresql');
	});

	it('falls back to sql', () => {
		expect(formatterLanguage(null)).toBe('sql');
	});
});
