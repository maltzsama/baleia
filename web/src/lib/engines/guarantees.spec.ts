import { describe, expect, it } from 'vitest';
import { makeEngine } from '../../test/factories';
import { describeGuarantees } from './guarantees';
import type { EngineCapabilities } from '$lib/engines/client';

function caps(overrides: Partial<EngineCapabilities> = {}): EngineCapabilities {
	return {
		dialect: 'trino',
		identity_modes: ['impersonation', 'connection'],
		read_only: 'enforced',
		supports_cancel: true,
		cancel_scope: 'any_process',
		transactions: true,
		multiple_result_sets: true,
		multiple_catalogs: true,
		namespace_depth: 3,
		session_schema: true,
		supports_explain: true,
		supports_time_travel: true,
		supports_catalog_ddl: true,
		monitoring: true,
		iceberg_maintenance: true,
		arrow_native: true,
		max_identifier_length: 255,
		identifier_case: 'lower',
		...overrides
	};
}

function text(engine: ReturnType<typeof makeEngine>): string {
	return describeGuarantees(engine)
		.map((guarantee) => `${guarantee.title}: ${guarantee.body}`)
		.join(' ');
}

describe('engine guarantees', () => {
	it('states enforced read-only, impersonation and cross-process cancel for Trino', () => {
		const engine = makeEngine({
			capabilities: caps(),
			identity_mode: 'impersonation',
			dialect: { name: 'trino', formatter_language: 'trino', codemirror_base: 'StandardSQL' }
		});
		const out = text(engine);
		expect(out).toContain('The engine itself rejects writes');
		expect(out).toContain('Queries run as each Baleia user');
		expect(out).toContain('Cancel works even if the worker running the query restarts');
		expect(out).toContain('Can join across catalogs');
		expect(out).toContain('folded to lower case; max 255 characters');
		expect(out).toContain('the editor uses trino formatting');
	});

	it('states credentials-based read-only and in-process cancel for a direct engine', () => {
		const engine = makeEngine({
			class: 'direct',
			kind: 'postgresql',
			identity_mode: 'connection',
			credentials_read_only: true,
			capabilities: caps({
				dialect: 'postgresql',
				read_only: 'none',
				cancel_scope: 'in_process',
				namespace_depth: 1,
				monitoring: false,
				iceberg_maintenance: false,
				arrow_native: false,
				identifier_case: 'lower',
				max_identifier_length: 63
			})
		});
		const out = text(engine);
		expect(out).toContain('the connection asserts its credentials only allow reads');
		expect(out).toContain("Queries run as the connection's user for everyone");
		expect(out).toContain('a query orphaned by a worker crash cannot be stopped');
		expect(out).not.toContain('Monitoring:');
	});

	it('says read-only is unavailable, and explains SQLite has no schemas or transactions', () => {
		const engine = makeEngine({
			class: 'direct',
			kind: 'sqlite',
			identity_mode: 'connection',
			credentials_read_only: false,
			capabilities: caps({
				dialect: 'sqlite',
				read_only: 'none',
				cancel_scope: 'in_process',
				transactions: false,
				namespace_depth: 0,
				session_schema: false,
				multiple_catalogs: false,
				identifier_case: 'preserve',
				max_identifier_length: 0,
				supports_time_travel: false
			})
		});
		const out = text(engine);
		expect(out).toContain('Read-only grants and agents cannot use this engine');
		expect(out).toContain('No transactions');
		expect(out).toContain('No schemas — tables are addressed by name only');
		expect(out).toContain('Default schema cannot be set per session');
		expect(out).toContain('One catalog per query');
	});

	it('says capabilities are unavailable when the adapter is missing', () => {
		const engine = makeEngine({ capabilities: null });
		const out = text(engine);
		expect(out).toContain('Baleia cannot state its guarantees');
	});
});
