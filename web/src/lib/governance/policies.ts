// Pure helpers of the governance screens (ADR 0028, #1146): which policies
// name a subject, how a table's policies combine for writes, and the mask
// templates that only fill in an expression.
import type {
	DataPolicy,
	DataPolicySubject,
	DataPolicyTemplate,
	DataPolicyWriteEffect,
	DataPolicyWrites
} from './api';

/** A stable key of one subject reference: `user:<id>`, `group:<name>`, `everyone`. */
export function subjectKey(subject: DataPolicySubject): string {
	if (subject.everyone) return 'everyone';
	if (subject.user) return `user:${subject.user}`;
	if (subject.team) return `team:${subject.team}`;
	if (subject.service_account) return `service_account:${subject.service_account}`;
	if (subject.group !== undefined) return `group:${subject.group}`;
	return '';
}

/** The subject reference a key names (the inverse of subjectKey). */
export function subjectFromKey(key: string): DataPolicySubject | null {
	if (key === 'everyone') return { everyone: true };
	const at = key.indexOf(':');
	if (at < 1) return null;
	const kind = key.slice(0, at);
	const value = key.slice(at + 1);
	if (!value) return null;
	switch (kind) {
		case 'user':
			return { user: value };
		case 'team':
			return { team: value };
		case 'service_account':
			return { service_account: value };
		case 'group':
			return { group: value };
		default:
			return null;
	}
}

/**
 * Whether a policy's subjects match someone known by `refs`: the keys of the
 * subject itself and of what it belongs to (a user's teams). "Everyone"
 * matches unless one of its exceptions is among the refs.
 */
export function policyApplies(policy: DataPolicy, refs: ReadonlySet<string>): boolean {
	return policy.subjects.some((subject) => {
		if (subject.everyone) {
			return !(subject.except ?? []).some((except) => refs.has(subjectKey(except)));
		}
		return refs.has(subjectKey(subject));
	});
}

/** `schema.table`, how the screens name a table. */
export function tableLabel(policy: Pick<DataPolicy, 'schema' | 'table'>): string {
	return `${policy.schema}.${policy.table}`;
}

/**
 * A key that keeps schema and table apart: names may hold dots, so schema
 * `a.b` with table `c` and schema `a` with table `b.c` stay two tables.
 */
export function tableKey(policy: Pick<DataPolicy, 'schema' | 'table'>): string {
	return JSON.stringify([policy.schema, policy.table]);
}

/** One table's policies: filters before masks, then by column and priority. */
export type TableGroup = {
	key: string;
	label: string;
	schema: string;
	table: string;
	policies: DataPolicy[];
};

/** Policies grouped by table, tables sorted by name. */
export function groupByTable(policies: readonly DataPolicy[]): TableGroup[] {
	const groups = new Map<string, DataPolicy[]>();
	for (const policy of policies) {
		const key = tableKey(policy);
		groups.set(key, [...(groups.get(key) ?? []), policy]);
	}
	const kindOrder = (p: DataPolicy) => (p.kind === 'row_filter' ? 0 : 1);
	return [...groups.entries()]
		.map(([key, list]) => ({
			key,
			label: tableLabel(list[0]),
			schema: list[0].schema,
			table: list[0].table,
			policies: [...list].sort(
				(a, b) =>
					kindOrder(a) - kindOrder(b) ||
					(a.column ?? '').localeCompare(b.column ?? '') ||
					b.priority - a.priority ||
					a.name.localeCompare(b.name)
			)
		}))
		.sort((a, b) => a.label.localeCompare(b.label) || a.key.localeCompare(b.key));
}

const effectRank: Record<DataPolicyWriteEffect, number> = {
	checked: 0,
	visible_rows: 1,
	refused: 2
};

/**
 * What writes do on a table once several of its policies apply: each write
 * takes the most restrictive effect (ADR 0028, "Writes on policied tables").
 */
export function combineWrites(policies: readonly DataPolicy[]): DataPolicyWrites | null {
	if (policies.length === 0) return null;
	const ops = ['insert', 'update', 'delete', 'merge'] as const;
	const out = { ...policies[0].writes };
	for (const policy of policies.slice(1)) {
		for (const op of ops) {
			if (effectRank[policy.writes[op]] > effectRank[out[op]]) out[op] = policy.writes[op];
		}
	}
	return out;
}

/** Templates the mask editor offers; custom leaves the expression as typed. */
export const maskTemplates = ['null_value', 'constant', 'partial', 'sha256', 'custom'] as const;

export type MaskOptions = {
	/** The column's SQL type, for the null template. */
	type?: string;
	/** The constant template's value, as text. */
	value?: string;
	/** The partial template: how many characters stay. */
	keep?: number;
	/** The partial template: keep the first or the last characters. */
	from?: 'start' | 'end';
};

// Always quoted: a column may be named after a reserved word (`order`).
function quoteIdentifier(column: string): string {
	return `"${column.replaceAll('"', '""')}"`;
}

function quoteString(value: string): string {
	return `'${value.replaceAll("'", "''")}'`;
}

/** Only a plain SQL type name is put into CAST; anything else falls back. */
function safeType(type: string | undefined): string {
	const trimmed = (type ?? '').trim();
	return /^[a-zA-Z][a-zA-Z0-9_ ]*(\(\s*\d+(\s*,\s*\d+)?\s*\))?$/.test(trimmed)
		? trimmed.toUpperCase()
		: 'VARCHAR';
}

/**
 * The expression a mask template fills in for a column. The result is an
 * ordinary expression in the restricted language: the server checks it like
 * any other, and the column's type must match (the partial and sha256
 * templates are for character columns).
 */
export function maskExpression(
	template: Exclude<DataPolicyTemplate, 'custom'>,
	column: string,
	options: MaskOptions = {}
): string {
	const col = quoteIdentifier(column);
	switch (template) {
		case 'null_value':
			return `CAST(NULL AS ${safeType(options.type)})`;
		case 'constant':
			return quoteString(options.value ?? '***');
		case 'partial': {
			const keep = Math.max(1, Math.floor(options.keep ?? 4));
			return options.from === 'end'
				? `concat('***', substr(${col}, -${keep}))`
				: `concat(substr(${col}, 1, ${keep}), '***')`;
		}
		case 'sha256':
			return `to_hex(sha256(to_utf8(${col})))`;
	}
}

/** What one table's enabled policies do for a subject. */
export type TableEffect = {
	/** tableKey of the table, unique within a catalog. */
	key: string;
	/** `schema.table`, for display. */
	table: string;
	/** The table has row policies, so it is default-deny (ADR 0028). */
	filtered: boolean;
	/** Permissive filters that match: the subject sees their union. */
	permissive: DataPolicy[];
	/** Restrictive filters that match: each narrows what is visible. */
	restrictive: DataPolicy[];
	/** No permissive filter matches a filtered table: no row is visible. */
	noRows: boolean;
	/** Per masked column, the matching masks, the applied one first. */
	masks: { column: string; applied: DataPolicy; shadowed: DataPolicy[] }[];
};

/**
 * The effect of a catalog's enabled policies on a subject, per table: only
 * tables where something applies are listed.
 */
export function effectsFor(
	policies: readonly DataPolicy[],
	refs: ReadonlySet<string>
): TableEffect[] {
	const out: TableEffect[] = [];
	for (const { key, label: table, policies: list } of groupByTable(
		policies.filter((p) => p.state === 'enabled')
	)) {
		const filters = list.filter((p) => p.kind === 'row_filter');
		const matching = filters.filter((p) => policyApplies(p, refs));
		const permissive = matching.filter((p) => p.mode !== 'restrictive');
		const restrictive = matching.filter((p) => p.mode === 'restrictive');
		const byColumn = new Map<string, DataPolicy[]>();
		for (const mask of list.filter((p) => p.kind === 'column_mask' && policyApplies(p, refs))) {
			const column = mask.column ?? '';
			byColumn.set(column, [...(byColumn.get(column) ?? []), mask]);
		}
		const masks = [...byColumn.entries()]
			.sort(([a], [b]) => a.localeCompare(b))
			.map(([column, items]) => {
				const sorted = [...items].sort((a, b) => b.priority - a.priority);
				return { column, applied: sorted[0], shadowed: sorted.slice(1) };
			});
		const filtered = filters.length > 0;
		if (!filtered && masks.length === 0) continue;
		out.push({
			key,
			table,
			filtered,
			permissive,
			restrictive,
			noRows: filtered && permissive.length === 0,
			masks
		});
	}
	return out;
}

/**
 * The rows a subject sees on a filtered table, as one expression: the union of
 * its permissive filters, narrowed by each restrictive one (ADR 0028). The
 * union is parenthesized as a whole so a restrictive filter narrows all of it.
 */
export function combinedFilter(effect: Pick<TableEffect, 'permissive' | 'restrictive'>): string {
	const parts: string[] = [];
	if (effect.permissive.length === 1) parts.push(`(${effect.permissive[0].expression})`);
	else if (effect.permissive.length > 1) {
		parts.push(`(${effect.permissive.map((p) => `(${p.expression})`).join(' OR ')})`);
	}
	for (const p of effect.restrictive) parts.push(`(${p.expression})`);
	return parts.join(' AND ');
}
