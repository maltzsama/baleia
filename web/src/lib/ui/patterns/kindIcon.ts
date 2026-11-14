// The single source of provider/engine kind literals (FD6). Every other file
// resolves icons and colors through `appearanceFor()`.
//
// A brand mark comes from the `simple-icons` package when it has one (imported
// by name, so the bundler drops the rest), else from an official SVG vendored
// under ./icons. Kinds with no brand fall back to a distinct line glyph.
import {
	siApachecassandra,
	siApachedoris,
	siApachedruid,
	siApachehive,
	siApachekafka,
	siClickhouse,
	siDatabricks,
	siDuckdb,
	siElasticsearch,
	siFaker,
	siGooglebigquery,
	siGooglesheets,
	siGrafana,
	siMariadb,
	siMongodb,
	siMysql,
	siOpensearch,
	siPostgresql,
	siPrometheus,
	siRedis,
	siSinglestore,
	siSnowflake,
	siSqlite,
	siTrino
} from 'simple-icons';
import exasolMark from './icons/exasol.svg';
import flightsqlMark from './icons/flightsql.svg';
import glueMark from './icons/glue.svg';
import icebergMark from './icons/iceberg.svg';
import igniteMark from './icons/ignite.svg';
import lakekeeperMark from './icons/lakekeeper.svg';
import nessieMark from './icons/nessie.svg';
import oracleMark from './icons/oracle.svg';
import pinotMark from './icons/pinot.svg';
import polarisMark from './icons/polaris.svg';
import redshiftMark from './icons/redshift.svg';
import sqlserverMark from './icons/sqlserver.svg';
import starrocksMark from './icons/starrocks.svg';

export type KindGlyph =
	| 'catalog'
	| 'metastore'
	| 'database'
	| 'engine'
	| 'file'
	| 'mountain'
	| 'nodes'
	| 'waves'
	| 'memory'
	| 'gauge'
	| 'bars'
	| 'void'
	| 'sliders';

export type KindAppearance = {
	color: string;
	glyph: KindGlyph;
	/** A vendored brand mark (raw SVG, original brand colours). */
	brand?: string;
	/** A `simple-icons` mark: its path on a 24x24 grid and its brand colour. */
	mark?: { path: string; hex: string };
	/** A dark mark: give it a light backdrop in dark mode so it stays legible. */
	contrast?: boolean;
	/** The brand colour is too light for the light theme: this darker shade of it
	 *  is drawn there instead, with no backdrop. */
	lightHex?: string;
};

// Relative luminance of a 6-digit hex colour (WCAG).
function luminance(hex: string): number {
	const [r, g, b] = [0, 2, 4].map((i) => {
		const c = parseInt(hex.slice(i, i + 2), 16) / 255;
		return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
	});
	return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

// Darkens a 6-digit hex colour, keeping its hue, until it holds 3:1 against
// white (relative luminance 0.3).
function darken(hex: string): string {
	const channels = [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16));
	for (let f = 0.95; f > 0.2; f -= 0.05) {
		const scaled = channels.map((c) => Math.round(c * f));
		const shade = scaled.map((c) => c.toString(16).padStart(2, '0')).join('');
		if (luminance(shade) <= 0.3) return shade.toUpperCase();
	}
	return hex;
}

// A package mark in its brand colour. How it holds up on each theme follows
// from the colour, so a new mark never needs it spelled out: a dark mark gets a
// light chip in dark mode, a light one a darker shade in light mode.
function simpleMark(
	icon: { path: string; hex: string },
	color: string,
	glyph: KindGlyph
): KindAppearance {
	const lum = luminance(icon.hex);
	return {
		color,
		glyph,
		mark: { path: icon.path, hex: icon.hex },
		contrast: lum < 0.1 || undefined,
		lightHex: lum > 0.4 ? darken(icon.hex) : undefined
	};
}

const appearances: Record<string, KindAppearance> = {
	polaris: { color: 'text-fuchsia-600', glyph: 'catalog', brand: polarisMark, contrast: true },
	nessie: { color: 'text-emerald-600', glyph: 'waves', brand: nessieMark },
	iceberg: { color: 'text-sky-600', glyph: 'mountain', brand: icebergMark },
	lakekeeper: {
		color: 'text-cyan-600',
		glyph: 'catalog',
		brand: lakekeeperMark,
		contrast: true
	},
	// Unity Catalog and Delta Lake are Databricks projects and share its mark.
	unity: simpleMark(siDatabricks, 'text-indigo-600', 'nodes'),
	delta: simpleMark(siDatabricks, 'text-red-600', 'database'),
	hive: simpleMark(siApachehive, 'text-orange-600', 'metastore'),
	glue: { color: 'text-amber-600', glyph: 'metastore', brand: glueMark },
	postgresql: simpleMark(siPostgresql, 'text-blue-600', 'database'),
	mysql: simpleMark(siMysql, 'text-orange-500', 'database'),
	mariadb: simpleMark(siMariadb, 'text-teal-600', 'database'),
	sqlserver: { color: 'text-red-600', glyph: 'database', brand: sqlserverMark },
	oracle: { color: 'text-rose-600', glyph: 'database', brand: oracleMark },
	sqlite: simpleMark(siSqlite, 'text-slate-600', 'file'),
	clickhouse: simpleMark(siClickhouse, 'text-yellow-600', 'database'),
	flightsql: { color: 'text-violet-600', glyph: 'engine', brand: flightsqlMark, contrast: true },
	trino: simpleMark(siTrino, 'text-pink-600', 'engine'),
	doris: simpleMark(siApachedoris, 'text-cyan-700', 'engine'),
	starrocks: { color: 'text-emerald-700', glyph: 'engine', brand: starrocksMark },
	bigquery: simpleMark(siGooglebigquery, 'text-blue-500', 'database'),
	snowflake: simpleMark(siSnowflake, 'text-sky-500', 'database'),
	redshift: { color: 'text-purple-600', glyph: 'database', brand: redshiftMark },
	singlestore: simpleMark(siSinglestore, 'text-purple-600', 'database'),
	exasol: { color: 'text-lime-600', glyph: 'database', brand: exasolMark, contrast: true },
	cassandra: simpleMark(siApachecassandra, 'text-sky-700', 'database'),
	mongodb: simpleMark(siMongodb, 'text-green-600', 'database'),
	redis: simpleMark(siRedis, 'text-red-500', 'database'),
	elasticsearch: simpleMark(siElasticsearch, 'text-teal-700', 'database'),
	opensearch: simpleMark(siOpensearch, 'text-blue-700', 'database'),
	druid: simpleMark(siApachedruid, 'text-cyan-500', 'engine'),
	pinot: { color: 'text-purple-700', glyph: 'engine', brand: pinotMark, contrast: true },
	duckdb: simpleMark(siDuckdb, 'text-yellow-500', 'database'),
	ignite: { color: 'text-red-600', glyph: 'engine', brand: igniteMark },
	kafka: simpleMark(siApachekafka, 'text-slate-700', 'waves'),
	// Loki is Grafana's: it has no mark of its own in the package.
	loki: simpleMark(siGrafana, 'text-orange-600', 'waves'),
	prometheus: simpleMark(siPrometheus, 'text-orange-700', 'gauge'),
	gsheets: simpleMark(siGooglesheets, 'text-green-600', 'file'),
	faker: simpleMark(siFaker, 'text-lime-700', 'file'),
	// Trino's built-in catalogs have no brand: a glyph each.
	memory: { color: 'text-slate-600 dark:text-slate-400', glyph: 'memory' },
	blackhole: { color: 'text-slate-700 dark:text-slate-400', glyph: 'void' },
	jmx: { color: 'text-amber-700', glyph: 'gauge' },
	tpch: { color: 'text-stone-600 dark:text-stone-400', glyph: 'bars' },
	tpcds: { color: 'text-stone-700 dark:text-stone-400', glyph: 'bars' },
	system: { color: 'text-zinc-600 dark:text-zinc-400', glyph: 'sliders' }
};

const familyFallback: Record<string, KindAppearance> = {
	catalog: { color: 'text-text-muted', glyph: 'catalog' },
	metastore: { color: 'text-text-muted', glyph: 'metastore' },
	database: { color: 'text-text-muted', glyph: 'database' },
	engine: { color: 'text-text-muted', glyph: 'engine' },
	file: { color: 'text-text-muted', glyph: 'file' }
};

// Dialect names that differ from the engine kind whose mark they show.
const dialectKinds: Record<string, string> = { tsql: 'sqlserver' };

/** The engine kind whose icon stands for a SQL dialect (the dialects the
 *  editor converts between): same name, except T-SQL, which is SQL Server. */
export function kindForDialect(dialect: string): string {
	return dialectKinds[dialect] ?? dialect;
}

export function appearanceFor(kind: string, family?: string): KindAppearance {
	return (
		appearances[kind] ??
		familyFallback[family ?? ''] ?? { color: 'text-text-muted', glyph: 'catalog' }
	);
}

// hasQueryProfile: the engine exposes a per-query profile (Trino). Kept here
// so no other file spells the kind (#557, FD6).
export function hasQueryProfile(kind: string | null | undefined): boolean {
	return kind === 'trino';
}

/** hasTrinoResourceGroups: the engine can hand its queue over to Trino's
 * resource groups (#600, #635); the API refuses the switch on any other kind. */
export function hasTrinoResourceGroups(kind: string | null | undefined): boolean {
	return kind === 'trino';
}

/** canOpenInBI: the engine kind a BI data-source file can point at (ADR 0017).
 * The BI gateway relays Trino's HTTP protocol and nothing else. */
export function canOpenInBI(kind: string): boolean {
	return kind === 'trino';
}
