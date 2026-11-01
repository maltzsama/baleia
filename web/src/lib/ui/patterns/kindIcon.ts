// The single source of provider/engine kind literals (FD6). Every other file
// resolves icons and colors through `appearanceFor()`.
//
// Brand marks are vendored under ./icons as monochrome silhouettes: the fill
// is `currentColor` (see the normalisation note in each file), so each icon
// takes the kind's text color and stays legible on both themes next to the
// line icons. Kinds without a mark fall back to a distinct line glyph.
import postgresqlMark from './icons/postgresql.svg?raw';
import mysqlMark from './icons/mysql.svg?raw';
import mariadbMark from './icons/mariadb.svg?raw';
import sqliteMark from './icons/sqlite.svg?raw';
import clickhouseMark from './icons/clickhouse.svg?raw';
import trinoMark from './icons/trino.svg?raw';
import hiveMark from './icons/hive.svg?raw';
import flightsqlMark from './icons/flightsql.svg?raw';
import dorisMark from './icons/doris.svg?raw';
import oracleMark from './icons/oracle.svg?raw';
import sqlserverMark from './icons/sqlserver.svg?raw';
import lakekeeperMark from './icons/lakekeeper.svg?raw';
import starrocksMark from './icons/starrocks.svg?raw';
import polarisMark from './icons/polaris.svg?raw';

export type KindGlyph =
	'catalog' | 'metastore' | 'database' | 'engine' | 'file' | 'mountain' | 'nodes' | 'waves';

export type KindAppearance = {
	color: string;
	glyph: KindGlyph;
	/** A vendored brand mark (raw SVG, original brand colours). */
	brand?: string;
	/** A dark mark: give it a light backdrop in dark mode so it stays legible. */
	contrast?: boolean;
};

const appearances: Record<string, KindAppearance> = {
	polaris: { color: 'text-fuchsia-600', glyph: 'catalog', brand: polarisMark, contrast: true },
	nessie: { color: 'text-emerald-600', glyph: 'waves' },
	iceberg: { color: 'text-sky-600', glyph: 'mountain' },
	lakekeeper: {
		color: 'text-cyan-600',
		glyph: 'catalog',
		brand: lakekeeperMark,
		contrast: true
	},
	unity: { color: 'text-indigo-600', glyph: 'nodes' },
	hive: { color: 'text-orange-600', glyph: 'metastore', brand: hiveMark },
	glue: { color: 'text-amber-600', glyph: 'metastore' },
	postgresql: { color: 'text-blue-600', glyph: 'database', brand: postgresqlMark },
	mysql: { color: 'text-orange-500', glyph: 'database', brand: mysqlMark },
	mariadb: { color: 'text-teal-600', glyph: 'database', brand: mariadbMark, contrast: true },
	sqlserver: { color: 'text-red-600', glyph: 'database', brand: sqlserverMark },
	oracle: { color: 'text-rose-600', glyph: 'database', brand: oracleMark },
	sqlite: { color: 'text-slate-600', glyph: 'file', brand: sqliteMark, contrast: true },
	clickhouse: { color: 'text-yellow-600', glyph: 'database', brand: clickhouseMark },
	flightsql: { color: 'text-violet-600', glyph: 'engine', brand: flightsqlMark, contrast: true },
	trino: { color: 'text-pink-600', glyph: 'engine', brand: trinoMark },
	doris: { color: 'text-cyan-700', glyph: 'engine', brand: dorisMark },
	starrocks: { color: 'text-emerald-700', glyph: 'engine', brand: starrocksMark }
};

const familyFallback: Record<string, KindAppearance> = {
	catalog: { color: 'text-text-muted', glyph: 'catalog' },
	metastore: { color: 'text-text-muted', glyph: 'metastore' },
	database: { color: 'text-text-muted', glyph: 'database' },
	engine: { color: 'text-text-muted', glyph: 'engine' },
	file: { color: 'text-text-muted', glyph: 'file' }
};

export function appearanceFor(kind: string, family?: string): KindAppearance {
	return (
		appearances[kind] ??
		familyFallback[family ?? ''] ?? { color: 'text-text-muted', glyph: 'catalog' }
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
