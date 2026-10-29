// Typed fixtures for page tests (#106). Every factory returns a complete object
// with sensible defaults; pass overrides for the case under test.
import type { components } from '$lib/api/schema';

type Workspace = components['schemas']['Workspace'];
type SavedQuery = components['schemas']['SavedQuery'];
type SemanticModel = components['schemas']['SemanticModel'];
type Space = components['schemas']['Space'];
type SpaceMember = components['schemas']['SpaceMember'];
type QueryFolder = components['schemas']['QueryFolder'];
type Engine = components['schemas']['Engine'];
type Me = components['schemas']['Me'];
type MetadataCatalog = components['schemas']['MetadataCatalog'];
type MetadataCatalogBinding = components['schemas']['MetadataCatalogBinding'];
type MetadataTableDetail = components['schemas']['MetadataTableDetail'];
type MetadataSearchResult = components['schemas']['MetadataSearchResult'];
type ConnectionProvider = components['schemas']['ConnectionProvider'];
type CatalogConnection = components['schemas']['CatalogConnection'];
type Catalog = components['schemas']['Catalog'];
type CatalogBinding = components['schemas']['CatalogBinding'];
type BindingSupport = components['schemas']['BindingSupport'];
type TestCatalogConnectionResult = components['schemas']['TestCatalogConnectionResult'];
type TableHealth = components['schemas']['TableHealth'];
type MaintenanceRun = components['schemas']['MaintenanceRun'];
type MaintenanceSettings = components['schemas']['MaintenanceSettings'];
type MaintenancePolicy = components['schemas']['MaintenancePolicy'];
type StoredMaintenancePolicy = components['schemas']['StoredMaintenancePolicy'];
type MaintenancePlan = components['schemas']['MaintenancePlan'];
type TableMaintenance = components['schemas']['TableMaintenance'];

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
		version: 1,
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
		auth_provider: 'local',
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

export function makeProvider(overrides: Partial<ConnectionProvider> = {}): ConnectionProvider {
	return {
		kind: 'testdb',
		label: 'Test Database',
		family: 'database',
		roles: ['metadata', 'direct_engine'],
		status: { state: 'supported' },
		config_schema: [
			{ key: 'host', label: 'Host', type: 'text', required: true, secret: false },
			{ key: 'port', label: 'Port', type: 'int', required: false, secret: false, default: '5432' },
			{ key: 'database', label: 'Database', type: 'text', required: true, secret: false },
			{ key: 'password', label: 'Password', type: 'secret', required: true, secret: true }
		],
		...overrides
	};
}

export function makeConnection(overrides: Partial<CatalogConnection> = {}): CatalogConnection {
	return {
		id: '00000000-0000-0000-0000-0000000000e1',
		name: 'analytics',
		type: 'testdb',
		url: 'jdbc:testdb://db:5432/analytics',
		config: { host: 'db', port: '5432', database: 'analytics' },
		has_credentials: true,
		default_readonly: true,
		credentials_read_only: false,
		created_at: '2026-01-01T00:00:00Z',
		updated_at: '2026-01-01T00:00:00Z',
		allowed_actions: ['view', 'update', 'delete'],
		...overrides
	};
}

export function makeCatalog(overrides: Partial<Catalog> = {}): Catalog {
	return {
		id: '00000000-0000-0000-0000-0000000000e2',
		connection_id: '00000000-0000-0000-0000-0000000000e1',
		name: 'analytics',
		trino_catalog_name: 'analytics',
		warehouse: 'analytics',
		connector: 'testdb',
		enabled: true,
		metadata_sync_status: 'finished',
		metadata_synced_at: '2026-01-01T00:00:00Z',
		allowed_actions: ['view', 'update', 'delete'],
		...overrides
	};
}

export function makeCatalogBinding(overrides: Partial<CatalogBinding> = {}): CatalogBinding {
	return {
		id: '00000000-0000-0000-0000-0000000000e3',
		catalog_id: '00000000-0000-0000-0000-0000000000e2',
		engine_id: '00000000-0000-0000-0000-000000000004',
		engine_catalog_name: 'analytics',
		connector: 'testdb',
		mode: 'managed',
		enabled: true,
		lifecycle_state: 'active',
		sync_status: 'synced',
		allowed_actions: ['view', 'update', 'delete'],
		...overrides
	};
}

export function makeBindingSupport(overrides: Partial<BindingSupport> = {}): BindingSupport {
	return {
		connectors: ['testdb'],
		protected_key_patterns: [],
		reserved_names: [],
		writes_supported: true,
		...overrides
	};
}

export function makeTestResult(
	overrides: Partial<TestCatalogConnectionResult> = {}
): TestCatalogConnectionResult {
	return {
		success: true,
		latency_ms: 12,
		checks: [
			{ name: 'metadata', ok: true, detail: '1 catalogs' },
			{ name: 'direct_engine', ok: true }
		],
		...overrides
	};
}

export function makeSemanticModel(overrides: Partial<SemanticModel> = {}): SemanticModel {
	return {
		id: '00000000-0000-0000-0000-0000000000d1',
		workspace_id: '00000000-0000-0000-0000-000000000001',
		space_id: '00000000-0000-0000-0000-000000000011',
		slug: 'orders',
		label: 'Orders',
		description: '',
		certified: false,
		catalog_id: '00000000-0000-0000-0000-0000000000e2',
		binding_id: '00000000-0000-0000-0000-0000000000e3',
		definition: {
			source: { kind: 'table', namespace: ['public'], table: 'orders' },
			primary_key: ['id'],
			dimensions: [
				{ name: 'id', type: 'int64' },
				{ name: 'status', label: 'Status', type: 'string' },
				{ name: 'created_at', label: 'Created at', type: 'timestamp', grains: ['day', 'month'] }
			],
			metrics: [{ name: 'revenue', label: 'Revenue', aggregation: 'sum', dimension: 'id' }]
		},
		format_version: 1,
		updated_at: '2026-01-01T00:00:00Z',
		version: 1,
		allowed_actions: ['view', 'update', 'delete'],
		...overrides
	};
}

export function makeTableHealth(overrides: Partial<TableHealth> = {}): TableHealth {
	return {
		catalog_id: '00000000-0000-0000-0000-0000000000c1',
		catalog_name: 'lake',
		namespace: 'sales',
		table_name: 'orders',
		health_score: 80,
		needs_compaction: false,
		needs_snapshot_expiry: false,
		due_operations: [],
		...overrides
	};
}

export function makeMaintenanceRun(overrides: Partial<MaintenanceRun> = {}): MaintenanceRun {
	return {
		id: '00000000-0000-0000-0000-0000000000a9',
		catalog_id: '00000000-0000-0000-0000-0000000000c1',
		namespace: 'sales',
		table_name: 'orders',
		operations: ['optimize'],
		status: 'finished',
		started_at: '2026-10-01T12:00:00Z',
		finished_at: '2026-10-01T12:05:00Z',
		workflow_id: 'wf-1',
		trigger: 'manual',
		reason: {},
		attempts: 1,
		...overrides
	};
}

export function makeMaintenanceSettings(
	overrides: Partial<MaintenanceSettings> = {}
): MaintenanceSettings {
	return {
		enabled: false,
		trigger: {},
		min_interval_seconds: 3600,
		max_interval_seconds: null,
		window: null,
		timeout_seconds: 1800,
		on_conflict: 'retry_with_backoff',
		params: {},
		...overrides
	};
}

export function makeMaintenancePolicy(
	overrides: Partial<MaintenancePolicy> = {}
): MaintenancePolicy {
	return {
		mode: 'managed',
		writer_commit_interval_seconds: 60,
		optimize: makeMaintenanceSettings(),
		optimize_manifests: makeMaintenanceSettings(),
		expire_snapshots: makeMaintenanceSettings({
			enabled: true,
			trigger: { snapshots: 100 },
			params: { retention_seconds: 86400, retain_last: 10 }
		}),
		remove_orphan_files: makeMaintenanceSettings(),
		...overrides
	};
}

export function makeStoredMaintenancePolicy(
	overrides: Partial<StoredMaintenancePolicy> = {}
): StoredMaintenancePolicy {
	return {
		...makeMaintenancePolicy(),
		revision: 1,
		created_at: '2026-10-01T12:00:00Z',
		updated_at: '2026-10-01T12:00:00Z',
		updated_by: null,
		...overrides
	};
}

export function makeMaintenancePlan(overrides: Partial<MaintenancePlan> = {}): MaintenancePlan {
	return {
		evaluated_at: '2026-10-01T12:00:00Z',
		policy_revision: 1,
		plan: [
			{ operation: 'optimize', due: false, would_run: false, reason: { blocked: 'disabled' } },
			{
				operation: 'optimize_manifests',
				due: false,
				would_run: false,
				reason: { blocked: 'disabled' }
			},
			{
				operation: 'expire_snapshots',
				due: true,
				would_run: true,
				reason: { fired: 'snapshots', measured: 140, threshold: 100 }
			},
			{
				operation: 'remove_orphan_files',
				due: false,
				would_run: false,
				reason: { blocked: 'disabled' }
			}
		],
		...overrides
	};
}

export function makeTableMaintenance(overrides: Partial<TableMaintenance> = {}): TableMaintenance {
	return {
		catalog_id: '00000000-0000-0000-0000-0000000000c1',
		namespace: 'sales',
		table_name: 'orders',
		retention_floor_seconds: 3600,
		format_version: 2,
		allowed_actions: ['view', 'update'],
		runs: [],
		policy: null,
		plan: null,
		...overrides
	};
}
