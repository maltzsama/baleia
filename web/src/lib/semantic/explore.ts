// Explore and model helpers for the semantic layer v2 (#91, #121). The browser
// never builds SQL: the explore query is the only state, it is serialized into
// ?q= as base64url(canonical JSON) (FD5) and compiled by the API.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type SemanticModel = components['schemas']['SemanticModel'];
export type SemanticModelCreate = components['schemas']['SemanticModelCreate'];
export type SemanticModelUpdate = components['schemas']['SemanticModelUpdate'];
export type ExploreQuery = components['schemas']['ExploreQuery'];
export type ExploreCompileResult = components['schemas']['ExploreCompileResult'];

export type ExploreFilter = NonNullable<ExploreQuery['filters']>[number];
export type ExploreSort = NonNullable<ExploreQuery['sorts']>[number];
export type FilterOp = ExploreFilter['op'];

export type LogicalType =
	'string' | 'boolean' | 'int64' | 'float64' | 'date' | 'timestamp' | 'timestamptz';
export type Relationship = 'many_to_one' | 'one_to_one' | 'one_to_many';
export type AggFunc = 'sum' | 'count' | 'count_distinct' | 'avg' | 'min' | 'max';

export type ModelDimension = {
	name: string;
	label?: string;
	sql?: string;
	type: LogicalType;
	grains?: string[];
	hidden?: boolean;
};
export type ModelMetric = {
	name: string;
	label?: string;
	aggregation?: AggFunc;
	dimension?: string;
	sql?: string;
	filters?: { column: string; op: string; values?: unknown[] }[];
	derived?: { left: string; op: '+' | '-' | '*' | '/'; right: string };
};
export type ModelJoin = {
	to: string;
	relationship: Relationship;
	type?: 'inner' | 'left';
	on: { from: string; to: string }[];
};
export type ModelSource = {
	kind: 'table' | 'sql';
	namespace?: string[];
	table?: string;
	sql?: string;
};
export type ModelDefinition = {
	label?: string;
	description?: string;
	source: ModelSource;
	primary_key?: string[];
	dimensions?: ModelDimension[];
	metrics?: ModelMetric[];
	calculated_fields?: { name: string; label?: string; sql: string }[];
	joins?: ModelJoin[];
	// The API stamps these on read; they are preserved on write.
	slug?: string;
	catalog_id?: string;
	binding_id?: string;
};

export const GRAINS = [
	'second',
	'minute',
	'hour',
	'day',
	'week',
	'month',
	'quarter',
	'year'
] as const;
export const AGGREGATIONS: AggFunc[] = ['sum', 'count', 'count_distinct', 'avg', 'min', 'max'];
export const MAX_EXPLORE_LIMIT = 10000;

export function definitionOf(model: SemanticModel): ModelDefinition {
	return (model.definition ?? {}) as unknown as ModelDefinition;
}

// ── errors ──────────────────────────────────────────────────────────────────────

export class SemanticRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string
	) {
		super(message);
		this.name = 'SemanticRequestError';
	}
}

function fail(error: unknown, status: number | undefined, fallback: string): never {
	// The compiler's message names the offending field; the localized text for the
	// code would drop it, so it is kept as the detail under the catalog headline.
	const body = error as { code?: string; message?: string } | undefined;
	throw new SemanticRequestError(
		body?.message ?? apiErrorMessage(error, fallback),
		status,
		body?.code
	);
}

/**
 * The compiler reports the offending field inside the message, quoted
 * (`unknown dimension "x"`). Returns the names from `known` that the message
 * mentions so the page can highlight those chips.
 */
export function offendingFields(message: string, known: string[]): string[] {
	const quoted = new Set([...message.matchAll(/"([^"]+)"/g)].map((match) => match[1]));
	return known.filter((name) => quoted.has(name));
}

// ── share link (FD5) ────────────────────────────────────────────────────────────

/** Field order matches Go's ExploreQuery so a link round-trips byte for byte. */
export function canonicalQuery(query: ExploreQuery): ExploreQuery {
	const out: Record<string, unknown> = { model: query.model };
	if (query.dimensions?.length) out.dimensions = query.dimensions;
	if (query.metrics?.length) out.metrics = query.metrics;
	if (query.filters?.length) {
		out.filters = query.filters.map((f) => ({
			field: f.field,
			op: f.op,
			...(f.values?.length ? { values: f.values } : {})
		}));
	}
	if (query.sorts?.length) {
		out.sorts = query.sorts.map((s) => ({ field: s.field, ...(s.desc ? { desc: true } : {}) }));
	}
	if (query.limit) out.limit = query.limit;
	if (query.time_grain) out.time_grain = query.time_grain;
	return out as ExploreQuery;
}

export function encodeShare(query: ExploreQuery): string {
	const bytes = new TextEncoder().encode(JSON.stringify(canonicalQuery(query)));
	let binary = '';
	for (const byte of bytes) binary += String.fromCharCode(byte);
	return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

const QUERY_KEYS = new Set([
	'model',
	'dimensions',
	'metrics',
	'filters',
	'sorts',
	'limit',
	'time_grain'
]);

function stringList(value: unknown): string[] | undefined {
	return Array.isArray(value) && value.every((item) => typeof item === 'string')
		? (value as string[])
		: undefined;
}

/** Decodes a ?q= value; null when it is not a valid explore query. */
export function decodeShare(value: string | null | undefined): ExploreQuery | null {
	if (!value) return null;
	let parsed: unknown;
	try {
		const base64 = value.replace(/-/g, '+').replace(/_/g, '/');
		const binary = atob(base64 + '='.repeat((4 - (base64.length % 4)) % 4));
		const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
		parsed = JSON.parse(new TextDecoder().decode(bytes));
	} catch {
		return null;
	}
	if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null;
	const raw = parsed as Record<string, unknown>;
	if (Object.keys(raw).some((key) => !QUERY_KEYS.has(key))) return null;
	if (typeof raw.model !== 'string' || !raw.model) return null;

	const dimensions = stringList(raw.dimensions);
	const metrics = stringList(raw.metrics);
	if ((raw.dimensions && !dimensions) || (raw.metrics && !metrics)) return null;
	if (raw.limit !== undefined && (!Number.isInteger(raw.limit) || (raw.limit as number) < 0))
		return null;
	if (raw.time_grain !== undefined && typeof raw.time_grain !== 'string') return null;

	const filters: ExploreFilter[] = [];
	for (const item of (raw.filters as unknown[] | undefined) ?? []) {
		const f = item as { field?: unknown; op?: unknown; values?: unknown };
		if (typeof f?.field !== 'string' || typeof f.op !== 'string') return null;
		if (f.values !== undefined && !Array.isArray(f.values)) return null;
		filters.push({ field: f.field, op: f.op as FilterOp, values: f.values as unknown[] });
	}
	const sorts: ExploreSort[] = [];
	for (const item of (raw.sorts as unknown[] | undefined) ?? []) {
		const s = item as { field?: unknown; desc?: unknown };
		if (typeof s?.field !== 'string') return null;
		sorts.push({ field: s.field, ...(s.desc ? { desc: true } : {}) });
	}
	return canonicalQuery({
		model: raw.model,
		dimensions,
		metrics,
		filters,
		sorts,
		limit: raw.limit as number | undefined,
		time_grain: raw.time_grain as string | undefined
	});
}

// ── field tree ──────────────────────────────────────────────────────────────────

export type FieldNode = {
	name: string;
	label: string;
	kind: 'dimension' | 'metric' | 'calculated';
	type?: LogicalType;
	grains: string[];
	model: string;
};
export type FieldGroup = {
	/** Slug of the model that owns the fields. */
	model: string;
	label: string;
	/** Set for joined models: the join name the fields are shown under. */
	join?: string;
	dimensions: FieldNode[];
	metrics: FieldNode[];
};

function groupOf(model: SemanticModel, join?: string): FieldGroup {
	const def = definitionOf(model);
	return {
		model: model.slug,
		label: model.label,
		join,
		dimensions: (def.dimensions ?? [])
			.filter((d) => !d.hidden)
			.map((d) => ({
				name: d.name,
				label: d.label || d.name,
				kind: 'dimension' as const,
				type: d.type,
				grains: d.grains ?? [],
				model: model.slug
			})),
		metrics: [
			...(def.metrics ?? []).map((mt) => ({
				name: mt.name,
				label: mt.label || mt.name,
				kind: 'metric' as const,
				grains: [],
				model: model.slug
			})),
			...(def.calculated_fields ?? []).map((cf) => ({
				name: cf.name,
				label: cf.label || cf.name,
				kind: 'calculated' as const,
				grains: [],
				model: model.slug
			}))
		]
	};
}

/** The base model's fields first, then each joined model under its join name. */
export function fieldTree(models: SemanticModel[], baseSlug: string): FieldGroup[] {
	const bySlug = new Map(models.map((model) => [model.slug, model]));
	const base = bySlug.get(baseSlug);
	if (!base) return [];
	const groups = [groupOf(base)];
	const seen = new Set([base.slug]);
	for (const join of definitionOf(base).joins ?? []) {
		const target = bySlug.get(join.to);
		if (!target || seen.has(target.slug)) continue;
		seen.add(target.slug);
		groups.push(groupOf(target, join.to));
	}
	return groups;
}

export function filterGroups(groups: FieldGroup[], search: string): FieldGroup[] {
	const q = search.trim().toLowerCase();
	if (!q) return groups;
	const match = (field: FieldNode) =>
		field.name.toLowerCase().includes(q) || field.label.toLowerCase().includes(q);
	return groups
		.map((group) => ({
			...group,
			dimensions: group.dimensions.filter(match),
			metrics: group.metrics.filter(match)
		}))
		.filter((group) => group.dimensions.length + group.metrics.length > 0);
}

export function allFields(groups: FieldGroup[]): Map<string, FieldNode> {
	const out = new Map<string, FieldNode>();
	for (const group of groups) {
		for (const field of [...group.dimensions, ...group.metrics]) out.set(field.name, field);
	}
	return out;
}

/** Filter operators by field kind and logical type (as in the metric builder, #115). */
export function operatorsFor(field: FieldNode | undefined): FilterOp[] {
	const nullOps: FilterOp[] = ['is_null', 'is_not_null'];
	if (!field || field.kind !== 'dimension') {
		return ['=', '!=', '>', '>=', '<', '<=', ...nullOps];
	}
	switch (field.type) {
		case 'boolean':
			return ['=', '!=', ...nullOps];
		case 'string':
			return ['=', '!=', 'in', 'not_in', ...nullOps];
		default:
			return ['=', '!=', '>', '>=', '<', '<=', 'in', 'not_in', ...nullOps];
	}
}

/** Typed values from the filter input: numbers and booleans are not strings on the wire. */
export function parseFilterValues(
	field: FieldNode | undefined,
	op: FilterOp,
	text: string
): unknown[] {
	if (op === 'is_null' || op === 'is_not_null') return [];
	const parts = op === 'in' || op === 'not_in' ? text.split(',') : [text];
	const values = parts.map((part) => part.trim()).filter((part) => part !== '');
	const numeric = field?.kind !== 'dimension' || field.type === 'int64' || field.type === 'float64';
	if (numeric)
		return values.map((value) => (Number.isFinite(Number(value)) ? Number(value) : value));
	if (field?.type === 'boolean') return values.map((value) => value === 'true');
	return values;
}

// ── saved query round trip ──────────────────────────────────────────────────────

const EXPLORE_MARK = '-- baleia:explore ';

/**
 * A saved query keeps the explore query as a leading comment so it re-opens in
 * Explore; the compiled SQL below it is what runs.
 */
export function withExploreComment(sql: string, query: ExploreQuery): string {
	return `${EXPLORE_MARK}/explore?q=${encodeShare(query)}\n${sql}`;
}

export function exploreLinkOf(sql: string): string | null {
	const first = sql.split('\n', 1)[0] ?? '';
	return first.startsWith(EXPLORE_MARK) ? first.slice(EXPLORE_MARK.length).trim() : null;
}

/**
 * The v2 model a v1 metric migrated into (#91): same slug, same table. The v1
 * page redirects there so old links keep working.
 */
export function migratedModelOf(
	models: SemanticModel[],
	metric: { slug: string; namespace: string[]; table: string; catalog_id: string }
): SemanticModel | undefined {
	return models.find((model) => {
		const def = definitionOf(model);
		return (
			model.catalog_id === metric.catalog_id &&
			def.source?.kind === 'table' &&
			def.source.table === metric.table &&
			(def.source.namespace ?? []).join('.') === metric.namespace.join('.') &&
			(def.metrics ?? []).some((mt) => mt.name === metric.slug)
		);
	});
}

// ── model helpers ───────────────────────────────────────────────────────────────

/** Maps a metadata logical type to a model dimension type; null when unsupported. */
export function dimensionTypeOf(logicalType: string | undefined): LogicalType | null {
	if (!logicalType) return null;
	if (logicalType.startsWith('decimal')) return 'float64';
	switch (logicalType) {
		case 'string':
		case 'boolean':
		case 'int64':
		case 'float64':
		case 'date':
		case 'timestamp':
		case 'timestamptz':
			return logicalType;
		default:
			return null;
	}
}

export function isTimeType(type: LogicalType | undefined): boolean {
	return type === 'date' || type === 'timestamp' || type === 'timestamptz';
}

export function defaultGrains(type: LogicalType): string[] {
	if (type === 'date') return ['day', 'week', 'month', 'quarter', 'year'];
	if (isTimeType(type)) return ['hour', 'day', 'week', 'month', 'quarter', 'year'];
	return [];
}

/** Dimensions generated from table columns; the author edits them afterwards. */
export function dimensionsFromColumns(
	columns: { name: string; logical_type?: string }[]
): ModelDimension[] {
	const out: ModelDimension[] = [];
	for (const column of columns) {
		const type = dimensionTypeOf(column.logical_type);
		if (!type) continue;
		out.push({
			name: column.name,
			label: column.name,
			type,
			...(isTimeType(type) ? { grains: defaultGrains(type) } : {})
		});
	}
	return out;
}

export function slugify(name: string): string {
	const base = name
		.toLowerCase()
		.normalize('NFKD')
		.replace(/[^a-z0-9]+/g, '_')
		.replace(/^_+|_+$/g, '')
		.slice(0, 63);
	if (!base) return '';
	return /^[a-z]/.test(base) ? base : `m_${base}`.slice(0, 63);
}

// ── API ─────────────────────────────────────────────────────────────────────────

export async function listModels(workspaceId: string, spaceId?: string): Promise<SemanticModel[]> {
	const { data, error, response } = await client.GET('/workspaces/{id}/semantic/models', {
		params: { path: { id: workspaceId }, query: spaceId ? { space_id: spaceId } : {} }
	});
	if (error || !data) fail(error, response?.status, 'Could not load the models');
	return data;
}

export async function getModel(workspaceId: string, modelId: string): Promise<SemanticModel> {
	const { data, error, response } = await client.GET(
		'/workspaces/{id}/semantic/models/{model_id}',
		{ params: { path: { id: workspaceId, model_id: modelId } } }
	);
	if (error || !data) fail(error, response?.status, 'Could not load the model');
	return data;
}

export async function createModel(
	workspaceId: string,
	body: SemanticModelCreate
): Promise<SemanticModel> {
	const { data, error, response } = await client.POST('/workspaces/{id}/semantic/models', {
		params: { path: { id: workspaceId } },
		body
	});
	if (error || !data) fail(error, response?.status, 'Could not create the model');
	return data;
}

export async function updateModel(
	workspaceId: string,
	modelId: string,
	body: SemanticModelUpdate
): Promise<SemanticModel> {
	const { data, error, response } = await client.PATCH(
		'/workspaces/{id}/semantic/models/{model_id}',
		{ params: { path: { id: workspaceId, model_id: modelId } }, body }
	);
	if (error || !data) fail(error, response?.status, 'Could not update the model');
	return data;
}

export async function deleteModel(workspaceId: string, modelId: string): Promise<void> {
	const { error, response } = await client.DELETE('/workspaces/{id}/semantic/models/{model_id}', {
		params: { path: { id: workspaceId, model_id: modelId } }
	});
	if (error) fail(error, response?.status, 'Could not delete the model');
}

export async function compileExplore(
	workspaceId: string,
	query: ExploreQuery,
	bindingId?: string | null
): Promise<ExploreCompileResult> {
	const { data, error, response } = await client.POST('/workspaces/{id}/semantic/explore/compile', {
		params: { path: { id: workspaceId } },
		body: { query: canonicalQuery(query), binding_id: bindingId || null }
	});
	if (error || !data) fail(error, response?.status, 'Could not compile the query');
	return data;
}
