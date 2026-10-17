// URL ↔ history filter math (#36). The filters live in the query string so a
// reload or a shared link restores the view; this module is pure so it can be
// tested without a SvelteKit runtime.
import type { operations } from '$lib/api/schema';

export type HistoryFilters = {
	status: string;
	q: string;
	from: string;
	to: string;
};

export type HistoryQuery = NonNullable<operations['listExecutions']['parameters']['query']>;

export function parseHistoryFilters(url: URL): HistoryFilters {
	return {
		status: url.searchParams.get('status') ?? '',
		q: url.searchParams.get('q') ?? '',
		from: url.searchParams.get('from') ?? '',
		to: url.searchParams.get('to') ?? ''
	};
}

/** Builds the API query; `from`/`to` are datetime-local values in the URL. */
export function historyQuery(filters: HistoryFilters, cursor?: string, limit = 50): HistoryQuery {
	return {
		limit,
		...(filters.status ? { status: filters.status as HistoryQuery['status'] } : {}),
		...(filters.q ? { q: filters.q } : {}),
		...(filters.from ? { from: new Date(filters.from).toISOString() } : {}),
		...(filters.to ? { to: new Date(filters.to).toISOString() } : {}),
		...(cursor ? { cursor } : {})
	};
}
