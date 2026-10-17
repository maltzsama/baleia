// Definitions in words and slugs (#115). Pure helpers, table-tested.
import type { MetricDefinition } from './client';

const AGGREGATION_LABELS: Record<string, string> = {
	sum: 'Sum',
	count: 'Count',
	count_distinct: 'Distinct count',
	avg: 'Average',
	min: 'Minimum',
	max: 'Maximum'
};

function quote(value: unknown): string {
	return typeof value === 'string' ? value : JSON.stringify(value);
}

export function describeDefinition(
	def: MetricDefinition,
	ref: { catalog: string; namespace: string[]; table: string },
	options: { dialect?: string } = {}
): string {
	const table = [ref.catalog, ...ref.namespace, ref.table].filter(Boolean).join(' · ');
	if (def.custom_sql) {
		return `Custom expression${options.dialect ? ` (\`${options.dialect}\` only)` : ''} from ${table}`;
	}
	const aggregation = AGGREGATION_LABELS[def.aggregation ?? ''] ?? def.aggregation ?? 'Aggregation';
	const measure =
		def.aggregation === 'count' && !def.column
			? 'all rows'
			: def.column
				? `\`${def.column}\``
				: 'all rows';
	let out = `${aggregation} of ${measure} from ${table}`;
	const filters = def.filters ?? [];
	if (filters.length > 0) {
		const parts = filters.map((filter) => {
			const values = filter.values ?? [];
			if (filter.op === 'is_null') return `\`${filter.column}\` is null`;
			if (filter.op === 'is_not_null') return `\`${filter.column}\` is not null`;
			if (filter.op === 'in')
				return `\`${filter.column}\` is one of ${values.map(quote).join(', ')}`;
			if (filter.op === 'not_in')
				return `\`${filter.column}\` is not one of ${values.map(quote).join(', ')}`;
			return `\`${filter.column}\` ${filter.op} ${values.map(quote).join(', ')}`;
		});
		out += `, where ${parts.join(' and ')}`;
	}
	if (def.time_column) {
		const grains = (def.time_grains ?? []).join(', ');
		out += `; time column \`${def.time_column}\`${grains ? ` (${grains})` : ''}`;
	}
	if (def.format && def.format.type === 'number') {
		out += `; shown with ${def.format.decimals} decimal(s)`;
	}
	return `${out}.`;
}

/** A slug from a label: lowercase, underscores, starts with a letter. */
export function slugify(label: string): string {
	let out = label
		.toLowerCase()
		.replace(/[^a-z0-9_]+/g, '_')
		.replace(/^_+|_+$/g, '')
		.slice(0, 63);
	if (!/^[a-z]/.test(out)) out = `m_${out}`;
	return out;
}
