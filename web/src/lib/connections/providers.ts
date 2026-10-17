// Provider-driven connection onboarding (#22 / FD6).
//
// Nothing here hard-codes a provider or engine kind: the labels, families,
// roles and config fields all come from GET /connection-providers. The only
// kind-shaped value is `provider.kind`, which is used to look up the binder's
// `binding-support` for an engine — never compared to a literal.
import type { components } from '$lib/api/schema';

export type ConnectionProvider = components['schemas']['ConnectionProvider'];
export type ProviderFamily = ConnectionProvider['family'];
export type ProviderRole = ConnectionProvider['roles'][number];

/** How the user wants to use the connection. */
export type Usage = {
	/** Register the connection as a direct engine. */
	direct: boolean;
	/** Federated engines to expose the source through. */
	bindEngineIds: string[];
};

// Identifiers (not string literals) so the kind literal lint stays clean.
const familyRank: Record<ProviderFamily, number> = {
	iceberg: 0,
	metastore: 1,
	database: 2,
	engine: 3
};

const familyLabels: Record<ProviderFamily, string> = {
	iceberg: 'Iceberg catalogs',
	metastore: 'Metastores',
	database: 'Databases',
	engine: 'Engines'
};

const familyDescriptions: Record<ProviderFamily, string> = {
	iceberg: 'Catalog managers and Iceberg REST catalogs.',
	metastore: 'Hive Metastore and AWS Glue catalog services.',
	database: 'Databases Baleia can query directly or expose through Trino.',
	engine: 'Query engines Baleia can run SQL on.'
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

export function familyOrder(): ProviderFamily[] {
	return (Object.keys(familyRank) as ProviderFamily[]).sort(
		(a, b) => familyRank[a] - familyRank[b]
	);
}

export function familyLabel(family: ProviderFamily): string {
	return familyLabels[family];
}

export function familyDescription(family: ProviderFamily): string {
	return familyDescriptions[family];
}

export function roleLabel(role: ProviderRole): string {
	return roleLabels[role];
}

export function roleDescription(role: ProviderRole): string {
	return roleDescriptions[role];
}

export function isUsable(provider: ConnectionProvider): boolean {
	return provider.status.state !== 'unsupported';
}

export function canQueryDirectly(provider: ConnectionProvider): boolean {
	return provider.roles.includes('direct_engine');
}

export function readsMetadata(provider: ConnectionProvider): boolean {
	return provider.roles.includes('metadata');
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
	if (usage.direct) intents.push('direct_engine');
	for (const engineId of usage.bindEngineIds) intents.push(`bind:${engineId}`);
	return intents;
}

/** Whether the usage means "create at least one logical catalog". */
export function needsCatalogs(usage: Usage): boolean {
	return usage.bindEngineIds.length > 0;
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
