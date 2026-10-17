// Typed audit reads (#49). The scope (own events vs owned workspaces vs all)
// is a server rule; the client only sends filters.
import { SvelteURLSearchParams } from 'svelte/reactivity';
import { apiErrorMessage, apiFetch, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type AuditEvent = components['schemas']['AuditEvent'];
export type AuditResolve = components['schemas']['AuditResolveResult'];
export type AuditPage = components['schemas']['AuditEventPage'];

export type AuditFilters = {
	from?: string;
	to?: string;
	actorId?: string;
	action?: string;
	resourceType?: string;
	resourceId?: string;
	outcome?: string;
	requestId?: string;
	traceId?: string;
	limit?: number;
	cursor?: string;
};

export class AuditRequestError extends Error {
	constructor(
		message: string,
		readonly code?: string
	) {
		super(message);
		this.name = 'AuditRequestError';
	}
}

function queryOf(filters: AuditFilters): Record<string, string | number> {
	const query: Record<string, string | number> = {};
	if (filters.from) query.from = filters.from;
	if (filters.to) query.to = filters.to;
	if (filters.actorId) query.actor_id = filters.actorId;
	if (filters.action) query.action = filters.action;
	if (filters.resourceType) query.resource_type = filters.resourceType;
	if (filters.resourceId) query.resource_id = filters.resourceId;
	if (filters.outcome) query.outcome = filters.outcome;
	if (filters.requestId) query.request_id = filters.requestId;
	if (filters.traceId) query.trace_id = filters.traceId;
	if (filters.limit) query.limit = filters.limit;
	if (filters.cursor) query.cursor = filters.cursor;
	return query;
}

export async function listAuditEvents(filters: AuditFilters): Promise<AuditPage> {
	const { data, error } = await client.GET('/audit/events', {
		params: { query: queryOf(filters) as never }
	});
	if (error || !data) {
		const body = error as { code?: string } | undefined;
		throw new AuditRequestError(
			apiErrorMessage(error, 'Could not load the audit trail'),
			body?.code
		);
	}
	return data;
}

export async function listAuditActions(): Promise<string[]> {
	const { data, error } = await client.GET('/audit/actions');
	if (error || !data)
		throw new AuditRequestError(apiErrorMessage(error, 'Could not load the actions'));
	return data;
}

export async function resolveAudit(body: {
	users?: string[];
	workspaces?: string[];
	connections?: string[];
	catalogs?: string[];
}): Promise<AuditResolve> {
	const { data, error } = await client.POST('/audit/resolve', { body });
	if (error || !data)
		throw new AuditRequestError(apiErrorMessage(error, 'Could not resolve names'));
	return data;
}

export function auditCsvUrl(filters: AuditFilters): string {
	const query = new SvelteURLSearchParams();
	for (const [key, value] of Object.entries(queryOf(filters))) {
		query.set(key, String(value));
	}
	return `/audit/events.csv?${query.toString()}`;
}

export async function downloadAuditCsv(filters: AuditFilters): Promise<Blob> {
	const res = await apiFetch(auditCsvUrl(filters));
	if (!res.ok) throw new AuditRequestError('Could not export the audit trail');
	return res.blob();
}
