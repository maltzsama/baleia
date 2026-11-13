// Provider-driven connection onboarding (#22 / FD6).
//
// Nothing here hard-codes a provider or engine kind: the labels, families,
// roles and config fields all come from GET /connection-providers. The only
// kind-shaped value is `provider.kind`, which is used to look up the binder's
// `binding-support` for an engine — never compared to a literal.
import type { components } from '$lib/api/schema';
import { m } from '$lib/paraglide/messages';
import { DIRECT, FEDERATED, type AccessMode } from './accessModes';

export type ConnectionProvider = components['schemas']['ConnectionProvider'];
export type ConnectionCategory = components['schemas']['ConnectionCategory'];
export type ProviderRole = ConnectionProvider['roles'][number];
export type { AccessMode };

/** How the user wants to use the connection. */
export type Usage = {
	/** The exclusive data access mode (ADR 0021 D.2), or null for engine-family types. */
	accessMode: AccessMode | null;
	/** Federated engines to expose the source through, when the mode is trino. */
	bindEngineIds: string[];
};

const roleLabels: Record<ProviderRole, string> = {
	metadata: 'Reads metadata',
	direct_engine: 'Query directly',
	federated_engine: 'Runs queries'
};

const roleDescriptions: Record<ProviderRole, string> = {
	metadata: 'Baleia lists its catalogs, schemas and tables after connecting.',
	direct_engine: 'Baleia can run SQL on it as its own query engine.',
	federated_engine: 'Baleia runs queries on it for federated catalogs.'
};

export function roleLabel(role: ProviderRole): string {
	return roleLabels[role];
}

export function roleDescription(role: ProviderRole): string {
	return roleDescriptions[role];
}

/** Statuses that cannot be created (ADR 0021 D.9 / CR-12). */
export function isUsable(provider: ConnectionProvider): boolean {
	return provider.status.state === 'supported' || provider.status.state === 'experimental';
}

/** A short badge label for the five implementation statuses. */
export function statusLabel(provider: ConnectionProvider): string {
	switch (provider.status.state) {
		case 'experimental':
			return m.connections_status_experimental();
		case 'planned':
			return m.connections_status_planned();
		case 'unsupported':
			return m.connections_status_unsupported();
		case 'not_applicable':
			return m.connections_status_not_applicable();
		default:
			return '';
	}
}

/** The type declares the direct access mode (ADR 0021 D.2). */
export function canDirect(provider: ConnectionProvider): boolean {
	return (provider.access_modes ?? []).includes(DIRECT);
}

/** The type declares the federated access mode (ADR 0029). */
export function canFederated(provider: ConnectionProvider): boolean {
	return (provider.access_modes ?? []).includes(FEDERATED);
}

export function readsMetadata(provider: ConnectionProvider): boolean {
	return provider.roles.includes('metadata');
}

/** The config key of a catalog manager that can authorize each user (#648). */
const userIdentityKey = 'user_identity';

/** Categories whose connections are catalog managers (ADR 0021 D.10). */
const catalogManagerCategory: Record<string, boolean> = {
	catalog_manager: true,
	metastore: true
};

/**
 * Who a catalog manager sees for the users of a connection (#648): each
 * Baleia user (`per_user`), or the connection's own account for every user
 * (`connection`, also every catalog manager that cannot authorize users).
 * `null` for connections that are not catalog managers.
 */
export function catalogIdentity(
	provider: ConnectionProvider | undefined,
	config: Record<string, unknown> | undefined
): 'per_user' | 'connection' | null {
	if (!provider || !provider.category || !catalogManagerCategory[provider.category]) return null;
	const supported = provider.config_schema.some((f) => f.key === userIdentityKey);
	return supported && config?.[userIdentityKey] === 'true' ? 'per_user' : 'connection';
}

/** A record of the provider's non-secret defaults, ready for the form. */
export function initialValues(provider: ConnectionProvider): Record<string, string> {
	const out: Record<string, string> = {};
	for (const field of provider.config_schema) {
		if (!field.secret && field.default) out[field.key] = field.default;
	}
	return out;
}

/**
 * Missing required fields for the current draft. In edit mode a stored secret
 * may be left blank: it is kept by a partial update.
 */
export function missingFields(
	provider: ConnectionProvider,
	values: Record<string, string>,
	secrets: Record<string, string>,
	editing = false,
	hasStoredSecrets = false
): string[] {
	const missing: string[] = [];
	for (const field of provider.config_schema) {
		if (!field.required) continue;
		if (field.secret) {
			if (!secrets[field.key] && !(editing && hasStoredSecrets)) missing.push(field.key);
		} else if (!values[field.key]?.trim()) {
			missing.push(field.key);
		}
	}
	return missing;
}

/** The checks the test endpoint should run for the chosen usage. */
export function intentsFor(provider: ConnectionProvider, usage: Usage): string[] {
	const intents: string[] = [];
	if (readsMetadata(provider)) intents.push('metadata');
	if (usage.accessMode === DIRECT) intents.push('direct_engine');
	for (const engineId of usage.bindEngineIds) intents.push(`bind:${engineId}`);
	return intents;
}

/** Whether the usage means "create at least one logical catalog". */
export function needsCatalogs(usage: Usage): boolean {
	return usage.accessMode === FEDERATED && usage.bindEngineIds.length > 0;
}

const engineNamePattern = /^[a-z][a-z0-9_-]{0,62}$/;
const catalogNamePattern = /^[a-z][a-z0-9_]{0,62}$/;

/** Engine names accept a hyphen; catalog names (SQL identifiers) do not. */
export function normalizeEngineName(value: string): string {
	const slug = value
		.toLowerCase()
		.replace(/[^a-z0-9_-]+/g, '_')
		.replace(/^[_-]+|[_-]+$/g, '')
		.slice(0, 62);
	if (!slug) return '';
	return engineNamePattern.test(slug) ? slug : `e_${slug}`.slice(0, 63);
}

export function normalizeCatalogName(value: string): string {
	const slug = value
		.toLowerCase()
		.replace(/[^a-z0-9_]+/g, '_')
		.replace(/^_+|_+$/g, '')
		.slice(0, 62);
	if (!slug) return '';
	return catalogNamePattern.test(slug) ? slug : `c_${slug}`.slice(0, 63);
}

/** The database name a connection points at, when the schema has one. */
export function databaseName(values: Record<string, string>): string {
	return (values.database ?? '').trim();
}
