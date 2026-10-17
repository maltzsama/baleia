import { describe, expect, it } from 'vitest';
import { describeError, errorCatalog } from './catalog';

const apiCodes = [
	'unauthorized',
	'forbidden',
	'not_found',
	'conflict',
	'validation_error',
	'rate_limited',
	'service_unavailable',
	'provider_unsupported',
	'connection_denied_network',
	'target_required',
	'engine_required',
	'target_unavailable',
	'read_only_unenforceable',
	'name_conflict',
	'slug_conflict_other_space',
	'property_not_overridable',
	'invalid_time_range',
	'not_supported',
	'version_conflict'
];

describe('error catalog', () => {
	it('has a plain-language entry for every API error code', () => {
		for (const code of apiCodes) {
			expect(errorCatalog[code], code).toBeDefined();
			expect(errorCatalog[code].title.length, code).toBeGreaterThan(0);
			expect(errorCatalog[code].explanation.length, code).toBeGreaterThan(0);
		}
	});

	it('falls back for unknown codes without throwing', () => {
		const described = describeError({ code: 'mystery', request_id: 'req-1' });
		expect(described.code).toBe('mystery');
		expect(described.title).toBe('Unexpected error');
		expect(described.requestId).toBe('req-1');
	});
});
