// Workbench autocomplete (#383): what fits at the cursor (sqlContext.ts) decides
// the source. Tables and columns come from the stored metadata of the tab's
// catalog, never from the engine; options are matched by prefix and ranked by
// kind, so a column of a table in the statement beats a function or keyword.
import type {
	Completion,
	CompletionContext,
	CompletionResult,
	CompletionSource
} from '@codemirror/autocomplete';
import type { EditorView } from '@codemirror/view';
import { keywordCompletionSource, type SQLDialect } from '@codemirror/lang-sql';
import { m } from '$lib/paraglide/messages';
import {
	loadNamespaces,
	loadTable,
	loadTables,
	searchMetadata,
	type MetadataColumn,
	type MetadataTableSummary
} from '$lib/metadata/client';
import { sqlContextAt, type SqlContext, type StatementTable } from './sqlContext';

/** What the active tab runs against. */
export type CompletionScope = {
	workspaceId: string;
	catalogId: string;
	bindingId: string;
	/** The catalog's name on the engine, for `catalog.schema.table`. */
	engineCatalog: string;
	/** Namespace levels before a table name (Trino: 1, a flat engine: 0). */
	namespaceDepth: number;
	/** The tab's schema, dot-separated; empty when the engine default applies. */
	schema: string;
	/** The engine dialect's lists; an empty keyword list uses `cmDialect`'s. */
	keywords: string[];
	functions: string[];
	cmDialect: SQLDialect;
	upperCase: boolean;
};

// Group order in the list, best first.
const RANK = { column: 0, table: 1, namespace: 2, otherTable: 3, function: 4, keyword: 5 } as const;
type Group = keyof typeof RANK;
type Option = Completion & { group: Group };

const STATEMENT_KEYWORDS = new Set([
	'SELECT',
	'WITH',
	'INSERT',
	'UPDATE',
	'DELETE',
	'MERGE',
	'CREATE',
	'ALTER',
	'DROP',
	'SHOW',
	'DESCRIBE',
	'EXPLAIN',
	'VALUES',
	'CALL',
	'USE',
	'SET',
	'GRANT',
	'REVOKE'
]);

// Names that must be quoted to be read as identifiers.
const RESERVED = new Set([
	...STATEMENT_KEYWORDS,
	'ALL',
	'AND',
	'AS',
	'BY',
	'CASE',
	'DISTINCT',
	'ELSE',
	'END',
	'FROM',
	'GROUP',
	'HAVING',
	'IN',
	'IS',
	'JOIN',
	'LIMIT',
	'NOT',
	'NULL',
	'ON',
	'OR',
	'ORDER',
	'TABLE',
	'THEN',
	'UNION',
	'WHEN',
	'WHERE'
]);

/** `name` as an identifier in SQL, quoted when it has to be. */
export function identifier(name: string, dialect: SQLDialect): string {
	if (/^[A-Za-z_][\w$]*$/.test(name) && !RESERVED.has(name.toUpperCase())) return name;
	const open = dialect.spec.identifierQuotes?.[0] ?? '"';
	const close = open === '[' ? ']' : open;
	return `${open}${name.split(close).join(close + close)}${close}`;
}

const lower = (value: string) => value.toLowerCase();
const samePath = (a: string[], b: string[]) =>
	a.length === b.length && a.every((part, i) => lower(part) === lower(b[i]));

/** Every table of a namespace (bounded), from the cached metadata pages. */
async function tablesIn(scope: CompletionScope, namespace: string[]) {
	const out: MetadataTableSummary[] = [];
	let cursor: string | undefined;
	for (let page = 0; page < 10; page++) {
		const result = await loadTables(scope.workspaceId, scope.catalogId, namespace, cursor, 500);
		out.push(...result.items);
		if (!result.next_cursor) break;
		cursor = result.next_cursor;
	}
	return out;
}

const schemaPath = (scope: CompletionScope) =>
	scope.namespaceDepth > 0 && scope.schema ? scope.schema.split('.') : [];

/** Drops a leading engine catalog name from a qualified path. */
function withoutCatalog(scope: CompletionScope, path: string[]): string[] | null {
	if (path.length <= scope.namespaceDepth) return path;
	if (path.length === scope.namespaceDepth + 1 && lower(path[0]) === lower(scope.engineCatalog))
		return path.slice(1);
	return null;
}

/** The metadata of a table named in the statement, or null if unknown. */
async function resolveTable(scope: CompletionScope, table: StatementTable) {
	if (table.path.length === 0) return null;
	const name = table.path[table.path.length - 1];
	const qualifier = withoutCatalog(scope, table.path.slice(0, -1));
	if (!qualifier) return null;
	const namespace = qualifier.length > 0 ? qualifier : schemaPath(scope);
	if (namespace.length !== scope.namespaceDepth) return null;
	const tables = await tablesIn(scope, namespace);
	const hit =
		tables.find((t) => t.name === name) ?? tables.find((t) => lower(t.name) === lower(name));
	return hit ? { summary: hit, namespace } : null;
}

async function columnsOf(scope: CompletionScope, table: StatementTable) {
	const resolved = await resolveTable(scope, table);
	if (!resolved) return null;
	const detail = await loadTable(
		scope.workspaceId,
		resolved.summary.id,
		scope.bindingId || undefined
	);
	return { name: resolved.summary.name, namespace: resolved.namespace, columns: detail.columns };
}

function info(title: string, type: string, lines: string[]): () => Node {
	return () => {
		const root = document.createElement('div');
		root.className = 'cm-baleia-info';
		const head = document.createElement('div');
		head.className = 'cm-baleia-info-head';
		const name = document.createElement('strong');
		name.textContent = title;
		head.append(name);
		if (type) {
			const kind = document.createElement('span');
			kind.className = 'cm-baleia-info-type';
			kind.textContent = type;
			head.append(kind);
		}
		root.append(head);
		for (const line of lines.filter(Boolean)) {
			const p = document.createElement('p');
			p.textContent = line;
			root.append(p);
		}
		return root;
	};
}

const columnType = (column: MetadataColumn) => column.display_type || column.native_type;

function columnOption(
	scope: CompletionScope,
	column: MetadataColumn,
	tables: string[],
	group: Group = 'column'
): Option {
	return {
		label: column.name,
		apply: identifier(column.name, scope.cmDialect),
		type: 'property',
		detail: columnType(column),
		info: info(column.name, columnType(column), [
			m.workbench_completion_column_of({ tables: tables.join(', ') }),
			column.doc?.description || column.comment || ''
		]),
		group
	};
}

function tableKind(kind: MetadataTableSummary['kind']): string {
	if (kind === 'view') return m.workbench_completion_view();
	if (kind === 'materialized_view') return m.workbench_completion_materialized_view();
	return m.workbench_completion_table();
}

function tableOption(
	scope: CompletionScope,
	table: MetadataTableSummary,
	namespace: string[],
	qualify: boolean
): Option {
	const schema = namespace.join('.');
	const name = identifier(table.name, scope.cmDialect);
	return {
		label: table.name,
		apply: qualify
			? [...namespace.map((part) => identifier(part, scope.cmDialect)), name].join('.')
			: name,
		type: table.kind === 'table' ? 'class' : 'interface',
		detail: schema,
		info: info(table.name, tableKind(table.kind), [
			schema ? m.workbench_completion_in_schema({ schema }) : '',
			table.comment ?? ''
		]),
		group: qualify ? 'otherTable' : 'table'
	};
}

function namespaceOption(scope: CompletionScope, path: string[]): Option {
	const name = path[path.length - 1];
	return {
		label: name,
		apply: identifier(name, scope.cmDialect),
		type: 'namespace',
		detail: m.workbench_completion_schema(),
		group: 'namespace'
	};
}

function functionOptions(scope: CompletionScope): Option[] {
	const seen = new Set<string>();
	const out: Option[] = [];
	for (const fn of [...scope.functions].sort((a, b) => lower(a).localeCompare(lower(b)))) {
		if (seen.has(lower(fn))) continue;
		seen.add(lower(fn));
		const label = scope.upperCase ? fn.toUpperCase() : fn;
		out.push({
			label,
			type: 'function',
			detail: m.workbench_completion_function(),
			apply: applyCall(label),
			group: 'function'
		});
	}
	return out;
}

/** Inserts `name()` with the cursor between the parentheses. */
function applyCall(name: string) {
	return (view: EditorView, _completion: Completion, from: number, to: number) => {
		const hasParen = view.state.sliceDoc(to, to + 1) === '(';
		const insert = hasParen ? name : `${name}()`;
		view.dispatch({
			changes: { from, to, insert },
			selection: { anchor: from + name.length + 1 },
			userEvent: 'input.complete'
		});
	};
}

async function keywordOptions(
	scope: CompletionScope,
	context: CompletionContext,
	statementStart: boolean
): Promise<Option[]> {
	let words: Completion[];
	if (scope.keywords.length > 0) {
		words = scope.keywords.map((keyword) => ({
			label: scope.upperCase ? keyword.toUpperCase() : keyword,
			type: 'keyword'
		}));
	} else {
		const result = await keywordCompletionSource(scope.cmDialect, scope.upperCase)(context);
		words = result?.options ? [...result.options] : [];
	}
	if (statementStart) {
		// Every statement keyword, also those the dialect's list lacks (SHOW on StandardSQL).
		const have = new Set(words.map((word) => word.label.toUpperCase()));
		for (const keyword of STATEMENT_KEYWORDS) {
			if (!have.has(keyword))
				words.push({ label: scope.upperCase ? keyword : lower(keyword), type: 'keyword' });
		}
	}
	return words
		.filter((word) => !statementStart || STATEMENT_KEYWORDS.has(word.label.toUpperCase()))
		.map((word) => ({ label: word.label, type: word.type, group: 'keyword' as const }));
}

/** Options for a table position: `FROM |`, `JOIN sch|`, `FROM schema.|`. */
async function tableOptions(
	scope: CompletionScope,
	ctx: SqlContext,
	signal: AbortSignal
): Promise<Option[]> {
	const qualifier = withoutCatalog(scope, ctx.qualifier);
	// `catalog.` lists that catalog's top-level namespaces.
	if (
		ctx.qualifier.length === 1 &&
		scope.namespaceDepth > 0 &&
		lower(ctx.qualifier[0]) === lower(scope.engineCatalog)
	) {
		const list = await loadNamespaces(scope.workspaceId, scope.catalogId, []);
		return list.items.map((item) => namespaceOption(scope, item.path));
	}
	if (!qualifier) return [];
	if (qualifier.length > 0) {
		if (qualifier.length < scope.namespaceDepth) {
			const list = await loadNamespaces(scope.workspaceId, scope.catalogId, qualifier);
			return list.items.map((item) => namespaceOption(scope, item.path));
		}
		const tables = await tablesIn(scope, qualifier);
		return tables.map((table) => tableOption(scope, table, qualifier, false));
	}

	const out: Option[] = [];
	const home = schemaPath(scope);
	if (home.length === scope.namespaceDepth) {
		for (const table of await tablesIn(scope, home))
			out.push(tableOption(scope, table, home, false));
	}
	if (scope.namespaceDepth > 0) {
		const list = await loadNamespaces(scope.workspaceId, scope.catalogId, []);
		out.push(...list.items.map((item) => namespaceOption(scope, item.path)));
	}
	// Tables of other schemas that start with the prefix, inserted qualified.
	if (ctx.prefix.length >= 2 && scope.namespaceDepth > 0) {
		await new Promise((resolve) => setTimeout(resolve, 200));
		if (signal.aborted) return out;
		const hits = await searchMetadata(scope.workspaceId, ctx.prefix, {
			kind: 'table',
			catalogId: scope.catalogId,
			limit: 50,
			signal
		});
		const byName = (a: string[], b: string[]) =>
			a.join('.').localeCompare(b.join('.'), undefined, { numeric: true });
		const others = hits
			.filter(
				(hit) => lower(hit.table).startsWith(lower(ctx.prefix)) && !samePath(hit.namespace, home)
			)
			.sort((a, b) => a.table.localeCompare(b.table) || byName(a.namespace, b.namespace));
		for (const hit of others) {
			out.push(
				tableOption(
					scope,
					{ id: hit.table_id, name: hit.table, kind: 'table' },
					hit.namespace,
					true
				)
			);
		}
	}
	return out;
}

/** Options for a column position; a qualifier picks one table of the statement. */
async function columnOptions(scope: CompletionScope, ctx: SqlContext): Promise<Option[] | null> {
	if (ctx.qualifier.length > 0) {
		const q = ctx.qualifier;
		const table =
			q.length === 1
				? (ctx.tables.find((t) => lower(t.alias) === lower(q[0])) ??
					ctx.tables.find(
						(t) => t.path.length > 0 && lower(t.path[t.path.length - 1]) === lower(q[0])
					))
				: ctx.tables.find((t) => t.path.length >= q.length && samePath(t.path.slice(-q.length), q));
		// An unknown qualifier gets nothing rather than some other table's columns.
		if (!table) return null;
		const resolved = await columnsOf(scope, table).catch(() => null);
		if (!resolved) return null;
		return resolved.columns.map((column) => columnOption(scope, column, [resolved.name]));
	}

	const byName = new Map<string, { column: MetadataColumn; tables: string[] }>();
	const resolved = await Promise.all(
		ctx.tables.map((table) => columnsOf(scope, table).catch(() => null))
	);
	for (const table of resolved) {
		if (!table) continue;
		for (const column of table.columns) {
			const entry = byName.get(lower(column.name));
			if (entry) entry.tables.push(table.name);
			else byName.set(lower(column.name), { column, tables: [table.name] });
		}
	}
	return [...byName.values()].map(({ column, tables }) => columnOption(scope, column, tables));
}

/** Prefix match, then group rank; exact matches first inside a group. */
function rank(options: Option[], prefix: string): Completion[] {
	const p = lower(prefix);
	const seen = new Set<string>();
	return options
		.filter((option) => lower(option.label).startsWith(p))
		.map((option, index) => ({ option, index }))
		.sort(
			(a, b) =>
				RANK[a.option.group] - RANK[b.option.group] ||
				Number(lower(b.option.label) === p) - Number(lower(a.option.label) === p) ||
				a.index - b.index
		)
		.filter(({ option }) => {
			// One entry per label and detail. A keyword that is also a function
			// (COUNT) is offered once, as the function, which ranks first.
			const label = lower(option.label);
			const key = `${option.group}:${label}:${option.detail ?? ''}`;
			if (seen.has(key) || (option.group === 'keyword' && seen.has(`function:${label}`)))
				return false;
			seen.add(key);
			if (option.group === 'function') seen.add(`function:${label}`);
			return true;
		})
		.map(({ option }) => {
			const { group, ...completion } = option;
			void group;
			return completion;
		});
}

/** The workbench completion source; `scope` reads the active tab's target. */
export function sqlCompletionSource(scope: () => CompletionScope | null): CompletionSource {
	return async (context: CompletionContext): Promise<CompletionResult | null> => {
		const current = scope();
		if (!current?.workspaceId) return null;
		const ctx = sqlContextAt(context.state.doc.toString(), context.pos);
		if (!ctx) return null;
		const typing = ctx.prefix.length > 0 || ctx.qualifier.length > 0;
		if (!typing && !context.explicit) return null;

		const controller = new AbortController();
		context.addEventListener('abort', () => controller.abort(), { onDocChange: true });

		let options: Option[] = [];
		try {
			switch (ctx.expect) {
				case 'statement':
					options = await keywordOptions(current, context, true);
					break;
				case 'keyword':
					options = await keywordOptions(current, context, false);
					break;
				case 'table':
					options = await tableOptions(current, ctx, controller.signal);
					break;
				case 'column': {
					const columns = await columnOptions(current, ctx);
					if (ctx.qualifier.length > 0) {
						options = columns ?? [];
						break;
					}
					options = [
						...(columns ?? []),
						...functionOptions(current),
						...(await keywordOptions(current, context, false))
					];
					break;
				}
			}
		} catch {
			// Aborted or the metadata is unavailable: nothing to suggest.
			return null;
		}
		if (context.aborted) return null;
		const ranked = rank(options, ctx.prefix);
		if (ranked.length === 0) return null;
		return {
			from: ctx.from,
			options: ranked,
			filter: false,
			getMatch: () => (ctx.prefix ? [0, ctx.prefix.length] : [])
		};
	};
}
