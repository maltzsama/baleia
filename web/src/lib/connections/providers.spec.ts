import { describe, expect, it } from 'vitest';
import { catalogIdentity, type ConnectionProvider } from './providers';

function provider(category: ConnectionProvider['category'], keys: string[]): ConnectionProvider {
	return {
		kind: 'k',
		label: 'K',
		family: 'iceberg',
		category,
		roles: ['metadata'],
		status: { state: 'supported' },
		config_schema: keys.map((key) => ({ key, label: key, type: 'text', required: false }))
	} as ConnectionProvider;
}

describe('catalogIdentity (#648)', () => {
	const perUserCapable = provider('catalog_manager', ['url', 'user_identity']);

	it('is per-user when the manager supports it and the connection turns it on', () => {
		expect(catalogIdentity(perUserCapable, { user_identity: 'true' })).toBe('per_user');
	});

	it('is the connection account when the flag is off', () => {
		expect(catalogIdentity(perUserCapable, { user_identity: 'false' })).toBe('connection');
		expect(catalogIdentity(perUserCapable, {})).toBe('connection');
	});

	it('flags managers that cannot authorize users as the connection account', () => {
		expect(catalogIdentity(provider('metastore', ['url']), {})).toBe('connection');
		expect(catalogIdentity(provider('catalog_manager', ['url']), { user_identity: 'true' })).toBe(
			'connection'
		);
	});

	it('says nothing for non-managers or an unknown provider', () => {
		expect(catalogIdentity(provider('relational', ['user_identity']), {})).toBeNull();
		expect(catalogIdentity(provider('engine', []), {})).toBeNull();
		expect(catalogIdentity(undefined, {})).toBeNull();
	});
});
