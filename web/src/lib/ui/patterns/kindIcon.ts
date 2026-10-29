// The single source of provider/engine kind literals (FD6). Every other file
// resolves icons and colors through `appearanceFor()`.

export type KindAppearance = {
	color: string;
	family: 'catalog' | 'metastore' | 'database' | 'engine' | 'file';
};

const appearances: Record<string, KindAppearance> = {
	polaris: { color: 'text-fuchsia-600', family: 'catalog' },
	nessie: { color: 'text-emerald-600', family: 'catalog' },
	iceberg: { color: 'text-sky-600', family: 'catalog' },
	lakekeeper: { color: 'text-cyan-600', family: 'catalog' },
	unity: { color: 'text-indigo-600', family: 'catalog' },
	hive: { color: 'text-orange-600', family: 'metastore' },
	glue: { color: 'text-amber-600', family: 'metastore' },
	postgresql: { color: 'text-blue-600', family: 'database' },
	mysql: { color: 'text-orange-500', family: 'database' },
	mariadb: { color: 'text-teal-600', family: 'database' },
	sqlserver: { color: 'text-red-600', family: 'database' },
	oracle: { color: 'text-rose-600', family: 'database' },
	sqlite: { color: 'text-slate-600', family: 'file' },
	clickhouse: { color: 'text-yellow-600', family: 'database' },
	flightsql: { color: 'text-violet-600', family: 'engine' },
	trino: { color: 'text-pink-600', family: 'engine' },
	doris: { color: 'text-cyan-700', family: 'engine' },
	starrocks: { color: 'text-emerald-700', family: 'engine' }
};

const familyFallback: Record<string, KindAppearance> = {
	catalog: { color: 'text-text-muted', family: 'catalog' },
	metastore: { color: 'text-text-muted', family: 'metastore' },
	database: { color: 'text-text-muted', family: 'database' },
	engine: { color: 'text-text-muted', family: 'engine' },
	file: { color: 'text-text-muted', family: 'file' }
};

export function appearanceFor(kind: string, family?: string): KindAppearance {
	return (
		appearances[kind] ??
		familyFallback[family ?? ''] ?? { color: 'text-text-muted', family: 'catalog' }
	);
}

// Connection kinds whose catalogs get Iceberg table health and maintenance
// (#500): the same list the health pass reads (ListEnabledIcebergCatalogs).
const tableHealthKinds = ['polaris', 'iceberg', 'nessie'];

/** Whether a catalog gets table health and maintenance: a source Iceberg catalog. */
export function hasTableHealth(catalog: {
	origin: string;
	provider_kind?: string | null;
}): boolean {
	return catalog.origin === 'source' && tableHealthKinds.includes(catalog.provider_kind ?? '');
}
