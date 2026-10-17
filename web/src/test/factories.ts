// Typed fixtures for page tests (#106). Every factory returns a complete object
// with sensible defaults; pass overrides for the case under test.
import type { components } from '$lib/api/schema';

type Workspace = components['schemas']['Workspace'];
type SavedQuery = components['schemas']['SavedQuery'];
type Engine = components['schemas']['Engine'];
type Me = components['schemas']['Me'];

export function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
	return {
		id: '00000000-0000-0000-0000-000000000001',
		name: 'Personal',
		type: 'personal',
		polaris_catalog_name: '',
		description: null,
		default_binding_id: null,
		default_schema: null,
		owner_user_id: null,
		owner_team_id: null,
		created_at: '2026-01-01T00:00:00Z',
		updated_at: '2026-01-01T00:00:00Z',
		allowed_actions: ['view', 'update', 'delete', 'manage_grants', 'set_defaults'],
		...overrides
	};
}

export function makeSavedQuery(overrides: Partial<SavedQuery> = {}): SavedQuery {
	return {
		id: '00000000-0000-0000-0000-000000000002',
		workspace_id: '00000000-0000-0000-0000-000000000001',
		name: 'Revenue',
		sql: 'SELECT 1',
		folder_id: null,
		created_by: '00000000-0000-0000-0000-000000000003',
		created_at: '2026-01-01T00:00:00Z',
		updated_at: '2026-01-01T00:00:00Z',
		...overrides
	};
}

export function makeEngine(overrides: Partial<Engine> = {}): Engine {
	return {
		id: '00000000-0000-0000-0000-000000000004',
		name: 'trino',
		kind: 'trino',
		class: 'federated',
		connection_id: '00000000-0000-0000-0000-000000000005',
		identity_mode: 'service',
		enabled: true,
		created_at: '2026-01-01T00:00:00Z',
		allowed_actions: ['view', 'update', 'delete'],
		...overrides
	} as Engine;
}

export function makeMe(overrides: Partial<Me> = {}): Me {
	return {
		id: '00000000-0000-0000-0000-000000000003',
		name: 'Ada Lovelace',
		email: 'ada@example.com',
		role: 'admin',
		auth_provider: 'password',
		created_at: '2026-01-01T00:00:00Z',
		features: { agents: false, assistant: false, retrievers: true },
		global_actions: [
			'create_connection',
			'create_workspace',
			'manage_users',
			'manage_identity',
			'view_all_audit',
			'view_system'
		],
		...overrides
	} as Me;
}

/** asViewer narrows a resource's allowed_actions to read-only. */
export function asViewer<T extends { allowed_actions?: string[] }>(resource: T): T {
	return { ...resource, allowed_actions: ['view'] };
}

/** asOwner grants the full action list a resource supports. */
export function asOwner<T extends { allowed_actions?: string[] }>(resource: T): T {
	return resource;
}
