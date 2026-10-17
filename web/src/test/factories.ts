// Typed fixtures for page tests (#106). Every factory returns a complete object
// with sensible defaults; pass overrides for the case under test.
import type { components } from '$lib/api/schema';

type Workspace = components['schemas']['Workspace'];
type SavedQuery = components['schemas']['SavedQuery'];
type Space = components['schemas']['Space'];
type SpaceMember = components['schemas']['SpaceMember'];
type QueryFolder = components['schemas']['QueryFolder'];
type Engine = components['schemas']['Engine'];
type Me = components['schemas']['Me'];
type MetadataCatalog = components['schemas']['MetadataCatalog'];
type MetadataCatalogBinding = components['schemas']['MetadataCatalogBinding'];
type MetadataTableDetail = components['schemas']['MetadataTableDetail'];
type MetadataSearchResult = components['schemas']['MetadataSearchResult'];

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
		space_id: '00000000-0000-0000-0000-000000000006',
		slug: 'revenue',
		name: 'Revenue',
		sql: 'SELECT 1',
		folder_id: null,
		created_by: '00000000-0000-0000-0000-000000000003',
		created_at: '2026-01-01T00:00:00Z',
		updated_at: '2026-01-01T00:00:00Z',
		...overrides
	};
}

export function makeSpace(overrides: Partial<Space> = {}): Space {
	return {
		id: '00000000-0000-0000-0000-000000000006',
		workspace_id: '00000000-0000-0000-0000-000000000001',
		name: 'General',
		slug: 'general',
		description: null,
		visibility: 'workspace',
		default_role: 'editor',
		is_default: true,
		my_role: 'manager',
		created_by: null,
		created_at: '2026-01-01T00:00:00Z',
		updated_at: '2026-01-01T00:00:00Z',
		...overrides
	};
}

export function makeFolder(overrides: Partial<QueryFolder> = {}): QueryFolder {
	return {
		id: '00000000-0000-0000-0000-000000000007',
		name: 'Monthly',
		workspace_id: '00000000-0000-0000-0000-000000000001',
		space_id: '00000000-0000-0000-0000-000000000006',
		slug: 'monthly',
		parent_id: null,
		created_at: '2026-01-01T00:00:00Z',
		...overrides
	};
}

export function makeSpaceMember(overrides: Partial<SpaceMember> = {}): SpaceMember {
	return {
		principal_type: 'user',
		principal_id: '00000000-0000-0000-0000-000000000003',
		role: 'viewer',
		granted_by: null,
		granted_at: '2026-01-01T00:00:00Z',
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

export function makeMetadataBinding(
	overrides: Partial<MetadataCatalogBinding> = {}
): MetadataCatalogBinding {
	return {
		binding_id: '00000000-0000-0000-0000-0000000000b1',
		engine_id: '00000000-0000-0000-0000-000000000004',
		engine_name: 'trino',
		engine_kind: 'trino',
		engine_catalog_name: 'lake_t',
		mode: 'adopted',
		status: 'active',
		...overrides
	};
}

export function makeMetadataCatalog(overrides: Partial<MetadataCatalog> = {}): MetadataCatalog {
	return {
		id: '00000000-0000-0000-0000-0000000000c1',
		name: 'lake',
		origin: 'native',
		provider_kind: 'polaris',
		metadata_synced_at: '2026-01-01T00:00:00Z',
		metadata_sync_status: 'finished',
		bindings: [makeMetadataBinding()],
		...overrides
	};
}

export function makeTableDetail(overrides: Partial<MetadataTableDetail> = {}): MetadataTableDetail {
	return {
		freshness: { metadata_synced_at: '2026-01-01T00:00:00Z' },
		id: '00000000-0000-0000-0000-0000000000d1',
		catalog_id: '00000000-0000-0000-0000-0000000000c1',
		namespace: ['sales'],
		name: 'orders',
		kind: 'table',
		properties: { format: 'iceberg', location: 's3://lake/orders' },
		last_changed_at: '2026-01-01T00:00:00Z',
		removed_at: null,
		binding_id: '00000000-0000-0000-0000-0000000000b1',
		qualified_name: '"lake_t"."sales"."orders"',
		preview_sql: 'SELECT * FROM "lake_t"."sales"."orders" LIMIT 100',
		columns: [
			{
				ordinal: 1,
				name: 'id',
				native_type: 'long',
				type_system: 'iceberg',
				display_type: 'bigint',
				nullable: false
			},
			{
				ordinal: 2,
				name: 'order_note',
				native_type: 'string',
				type_system: 'iceberg',
				display_type: 'varchar',
				nullable: true,
				comment: 'Free text'
			}
		],
		...overrides
	};
}

export function makeSearchResult(
	overrides: Partial<MetadataSearchResult> = {}
): MetadataSearchResult {
	return {
		kind: 'table',
		table_id: '00000000-0000-0000-0000-0000000000d1',
		catalog_id: '00000000-0000-0000-0000-0000000000c1',
		namespace: ['sales'],
		table: 'orders',
		column: null,
		native_type: null,
		score: 0.9,
		metadata_synced_at: '2026-01-01T00:00:00Z',
		...overrides
	};
}

/** asViewer narrows a resource's allowed_actions to read-only. */
export function asViewer<T extends { allowed_actions?: string[] }>(resource: T): T {
	return { ...resource, allowed_actions: ['view'] };
}

/** asOwner grants the full action list a resource supports. */
export function asOwner<T extends { allowed_actions?: string[] }>(resource: T): T {
	return resource;
}
