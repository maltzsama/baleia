import { describe, expect, it } from 'vitest';
import { unsupportedReason } from './unsupported';

describe('unsupportedReason', () => {
	it('names the table kind the source cannot serve (#977)', () => {
		const text = unsupportedReason({ code: 'table_kind', detail: 'TABLE_DB_STORAGE' });
		expect(text).toContain('type: TABLE_DB_STORAGE');
		expect(text).toContain('Unsupported type');
	});

	it('names the registration the engine does not recognize (#980)', () => {
		const text = unsupportedReason({ code: 'registration', detail: 'table_type=DELTA' });
		expect(text).toContain('registered as: table_type=DELTA');
		expect(text).toContain('spark.sql.sources.provider=delta');
	});

	it('falls back to a refusal for a table the source refused', () => {
		expect(unsupportedReason({ code: 'refused' })).toContain('refused');
	});
});
