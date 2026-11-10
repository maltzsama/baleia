// Dialect-aware wrapper over `sqlingo` — the TypeScript port of SQLGlot — for
// the editor's SQL intelligence (#756). sqlingo is large (~15 MB unpacked) and
// pulls in `luxon`, so nothing imports it eagerly: every entry point loads the
// dialect on first use and caches it. A regression in the port, or a future
// swap, is contained in this module.

import type { Dialect, Expression } from 'sqlingo';

type DialectClass = typeof Dialect;

/** A token in the shape the workbench glue consumes. Offsets are UTF-16 code
 *  units (JavaScript string indices); `end` is exclusive. */
export type Token = {
	kind: 'word' | 'quoted' | 'string' | 'comment' | 'number' | 'punct';
	value: string;
	start: number;
	end: number;
};

// Baleia dialect name -> sqlingo dialect export. Dialects without a dedicated
// grammar (mariadb runs as mysql, flightsql-generic is generic) fall through to
// sqlingo's base Dialect.
const DIALECTS: Record<string, () => Promise<DialectClass>> = {
	trino: async () => (await import('sqlingo/trino')).Trino,
	presto: async () => (await import('sqlingo/presto')).Presto,
	postgresql: async () => (await import('sqlingo/postgres')).Postgres,
	mysql: async () => (await import('sqlingo/mysql')).MySQL,
	mariadb: async () => (await import('sqlingo/mysql')).MySQL,
	tsql: async () => (await import('sqlingo/tsql')).TSQL,
	oracle: async () => (await import('sqlingo/oracle')).Oracle,
	sqlite: async () => (await import('sqlingo/sqlite')).SQLite,
	clickhouse: async () => (await import('sqlingo/clickhouse')).ClickHouse,
	starrocks: async () => (await import('sqlingo/starrocks')).StarRocks,
	doris: async () => (await import('sqlingo/doris')).Doris,
	dremio: async () => (await import('sqlingo/dremio')).Dremio
};

/** The dialects the editor maps, in selector order. Deriving them from the
 *  `DIALECTS` keys keeps engine-kind literals out of the callers (FD6). */
export const DIALECT_NAMES: readonly string[] = Object.keys(DIALECTS);

/** The default conversion target, when no source dialect is known. */
export const DEFAULT_TARGET_DIALECT = DIALECT_NAMES[0] ?? '';

/** A target dialect that differs from `source`, so "Convert" is never a no-op
 *  when the current engine already speaks the natural default. */
export function otherDialect(source: string | null | undefined): string {
	return DIALECT_NAMES.find((name) => name !== source) ?? DEFAULT_TARGET_DIALECT;
}

const dialectCache = new Map<string, Promise<DialectClass>>();

/** Dialects that treat `#` as a line comment (the MySQL family). Keys are
 *  identifiers, not literals, so they do not trip the FD6 engine-kind rule. */
const HASH_COMMENT: Record<string, boolean> = {
	mysql: true,
	mariadb: true
};

/** Whether the Baleia dialect treats `#` as a line comment (MySQL, MariaDB). */
export function usesHashComments(name: string | null | undefined): boolean {
	return HASH_COMMENT[name ?? ''] === true;
}

/** The sqlingo dialect for a Baleia dialect name, cached and lazy. */
export function loadDialect(name: string | null | undefined): Promise<DialectClass> {
	const key = name ?? 'generic';
	const cached = dialectCache.get(key);
	if (cached) return cached;
	const loader = DIALECTS[key];
	const pending = (loader ? loader() : import('sqlingo').then((mod) => mod.Dialect)).catch(
		(error: unknown) => {
			dialectCache.delete(key);
			throw error;
		}
	);
	dialectCache.set(key, pending);
	return pending;
}

// sqlingo's Token carries a string-enum `tokenType`; we only read these fields.
type RawToken = { tokenType: string; text: string; start: number; end: number };

// sqlingo's string-like token types, none of which is ever a word boundary.
const STRING_TYPES = new Set([
	'string',
	'bitString',
	'hexString',
	'byteString',
	'nationalString',
	'rawString',
	'heredocString',
	'unicodeString'
]);

const WORD = /^[A-Za-z_][\w$]*$/;

// sqlingo merges "<X> BY" clauses into one token; the editor tracks the BY,
// which opens a column/expression list (GROUP BY, ORDER BY, PARTITION BY, …).
const BY_CLAUSES = new Set([
	'groupBy',
	'orderBy',
	'partitionBy',
	'connectBy',
	'orderSiblingsBy',
	'clusterBy',
	'distributeBy',
	'sortBy'
]);

// sqlingo's offsets are inclusive (`end` is the last code unit); the glue wants
// exclusive, so `end + 1`.
function normalize(raw: RawToken): Token {
	const end = raw.end + 1;
	if (STRING_TYPES.has(raw.tokenType))
		return { kind: 'string', value: raw.text, start: raw.start, end };
	if (raw.tokenType === 'comment')
		return { kind: 'comment', value: raw.text, start: raw.start, end };
	if (raw.tokenType === 'number') return { kind: 'number', value: raw.text, start: raw.start, end };
	if (raw.tokenType === 'identifier' || raw.tokenType === 'objectIdentifier')
		return { kind: 'quoted', value: raw.text, start: raw.start, end };
	if (raw.tokenType === 'semicolon') return { kind: 'punct', value: ';', start: raw.start, end };
	if (BY_CLAUSES.has(raw.tokenType)) return { kind: 'word', value: 'BY', start: raw.start, end };
	// `var` and keyword tokens (SELECT, FROM, int, …) are word-like; a keyword's
	// text is always word characters, so the shape test catches it without
	// enumerating sqlingo's keyword table.
	if (raw.tokenType === 'var' || WORD.test(raw.text))
		return { kind: 'word', value: raw.text, start: raw.start, end };
	return { kind: 'punct', value: raw.text, start: raw.start, end };
}

/** Tokenize `text` with the dialect's rules. Throws on unterminated input (an
 *  open string, comment or quoted identifier); callers decide how to degrade. */
export async function tokenize(name: string | null | undefined, text: string): Promise<Token[]> {
	const dialect = await loadDialect(name);
	const { tokenize: run } = await import('sqlingo');
	return (run(text, dialect) as unknown as RawToken[]).map(normalize);
}

/** Split `text` into statements, as [start, end) offsets into the UTF-16 text.
 *  Spans with no code (only whitespace, `;` or comments) are dropped, so a
 *  cursor after a trailing comment falls back to the last executable statement.
 *  sqlingo strips comments from the token stream, so a comment-only span holds
 *  no tokens at all. */
export async function statementSpans(
	name: string | null | undefined,
	text: string
): Promise<{ start: number; end: number }[]> {
	const tokens = await tokenize(name, text);
	const spans: { start: number; end: number }[] = [];
	let start = 0;
	for (const token of tokens) {
		if (token.kind === 'punct' && token.value === ';') {
			spans.push({ start, end: token.end });
			start = token.end;
		}
	}
	spans.push({ start, end: text.length });
	const isCode = (token: Token) => !(token.kind === 'punct' && token.value === ';');
	return spans.filter((span) =>
		tokens.some((token) => token.start >= span.start && token.end <= span.end && isCode(token))
	);
}

// Parse `text` with the `read` grammar and render every statement with the
// `write` grammar. The default (raising) error level rejects malformed SQL, so a
// partial parse is never rewritten into different SQL; the filter only drops the
// `undefined` sqlingo yields for empty input.
async function render(read: DialectClass, write: DialectClass, text: string): Promise<string> {
	const { parse } = await import('sqlingo');
	const expressions = parse(text, { read });
	return expressions
		.filter((expression): expression is Expression => expression != null)
		.map((expression) => expression.sql({ dialect: write, pretty: true }))
		.join(';\n');
}

/** Format `text` for its dialect, preserving comments and whitespace intent. A
 *  malformed script throws rather than being rewritten, so the editor keeps the
 *  original text. */
export async function formatSQL(name: string | null | undefined, text: string): Promise<string> {
	const dialect = await loadDialect(name);
	return render(dialect, dialect, text);
}

/** Transpile `text` from the `source` dialect to the `target` dialect. A
 *  malformed script throws rather than being half-rewritten (the caller keeps
 *  the original). sqlingo lowers dialect-specific constructs where it can, for
 *  example PostgreSQL `ILIKE` becomes `LOWER(...) LIKE LOWER(...)` on Trino. */
export async function transpileSQL(
	source: string | null | undefined,
	target: string | null | undefined,
	text: string
): Promise<string> {
	const read = await loadDialect(source);
	const write = await loadDialect(target);
	return render(read, write, text);
}

/** Rewrite `text` with sqlingo's rule-based optimizer, in its own dialect. A
 *  malformed script throws rather than being rewritten (the caller keeps the
 *  original). This folds constants and hoists subqueries, it is not an engine
 *  planner: no statistics or cost model are involved. */
export async function optimizeSQL(name: string | null | undefined, text: string): Promise<string> {
	const dialect = await loadDialect(name);
	const { optimize } = await import('sqlingo');
	return optimize(text, { dialect }).sql({ dialect, pretty: true });
}
