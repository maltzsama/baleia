import { describe, expect, it } from 'vitest';
import { describeError, errorCatalog } from './catalog';

const apiCodes = [
	'unauthorized',
	'forbidden',
	'not_found',
	'conflict',
	'validation_error',
	'invalid_definition',
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

	// The codes internal/queryengine stores on a failed statement (#393).
	it('describes every engine statement code as a statement error', () => {
		for (const code of [
			'syntax',
			'not_found',
			'permission_denied',
			'read_only_unsupported',
			'timeout',
			'cancelled',
			'resource_exhausted',
			'engine_unavailable',
			'conflict',
			'interrupted',
			'internal'
		]) {
			expect(errorCatalog[`statement.${code}`], code).toBeDefined();
			expect(describeError({ code }, 'statement').code, code).toBe(code);
		}
		// A statement's not_found is a missing name, not a deleted resource.
		expect(describeError({ code: 'not_found' }, 'statement').title).toBe('Name not found');
		expect(describeError({ code: 'not_found' }).title).toBe('Not found');
	});

	it('falls back for unknown codes without throwing', () => {
		const described = describeError({ code: 'mystery', request_id: 'req-1' });
		expect(described.code).toBe('mystery');
		expect(described.title).toBe('Unexpected error');
		expect(described.requestId).toBe('req-1');
	});
});
