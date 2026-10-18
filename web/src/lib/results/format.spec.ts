import { describe, expect, it } from 'vitest';
import { Field, Int64, TimestampMillisecond, Utf8 } from 'apache-arrow';
import {
	cellText,
	columnMeta,
	formatCell,
	formatDate,
	formatDecimal,
	formatTimestamp,
	previewJson,
	UNLOADED
} from './format';

describe('formatDecimal', () => {
	it('renders the exact decimal text', () => {
		expect(formatDecimal(12345n, 2)).toBe('123.45');
		expect(formatDecimal(-42n, 2)).toBe('-0.42');
		expect(formatDecimal(7n, 0)).toBe('7');
	});
});

describe('unloaded cells', () => {
	it('renders and copies blanks, never as SQL NULL', () => {
		const column = columnMeta(new Field('n', new Int64(), true));
		expect(formatCell(UNLOADED, column)).toEqual({
			text: '',
			align: 'left',
			null: false,
			nested: false
		});
		expect(cellText(UNLOADED, column)).toBe('');
	});
});

describe('formatTimestamp', () => {
	it('renders ISO 8601 from every unit', () => {
		const millis = Date.parse('2020-01-01T00:00:00.000Z');
		expect(formatTimestamp(millis, 1)).toBe('2020-01-01T00:00:00.000Z');
		expect(formatTimestamp(millis * 1000, 2)).toBe('2020-01-01T00:00:00.000Z');
		expect(formatTimestamp(millis / 1000, 0)).toBe('2020-01-01T00:00:00.000Z');
	});
});

describe('formatDate', () => {
	it('renders a day-based date', () => {
		expect(formatDate(18262, 0)).toBe('2020-01-01');
	});
});

describe('columnMeta', () => {
	it('reads the native type metadata and maps the Arrow type', () => {
		const field = Field.new('id', new Int64(), true, new Map([['baleia.native_type', 'BIGINT']]));
		const meta = columnMeta(field);
		expect(meta.name).toBe('id');
		expect(meta.nativeType).toBe('BIGINT');
		expect(meta.kind).toBe('number');
		expect(meta.nested).toBe(false);
	});

	it('uses the dictionary value type and the timestamp unit', () => {
		const text = columnMeta(Field.new('note', new Utf8()));
		expect(text.kind).toBe('string');
		const ts = columnMeta(Field.new('at', new TimestampMillisecond()));
		expect(ts.kind).toBe('timestamp');
		expect(ts.unit).toBe(1);
	});
});

describe('formatCell', () => {
	const number = columnMeta(Field.new('n', new Int64()));
	it('styles nulls and aligns numbers right', () => {
		expect(formatCell(null, number)).toEqual({
			text: 'NULL',
			align: 'right',
			null: true,
			nested: false
		});
		expect(formatCell(42n, number)).toEqual({
			text: '42',
			align: 'right',
			null: false,
			nested: false
		});
	});

	it('collapses nested values with a JSON preview', () => {
		const nested = {
			kind: 'nested' as const,
			name: 'tags',
			type: 'List<Utf8>',
			nativeType: '',
			nested: true
		};
		const display = formatCell(['a', 'b'], nested);
		expect(display.text).toBe('["a","b"]');
		expect(display.nested).toBe(true);
		expect(previewJson({ a: 1n }, 4)).toBe('{"a"…');
	});

	it('returns an empty clipboard value for null', () => {
		expect(cellText(null, number)).toBe('');
	});
});
