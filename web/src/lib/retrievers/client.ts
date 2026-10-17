// Typed retriever calls (#118). The scheduler, cron preview and destination
// eligibility are server decisions; the UI never evaluates cron itself.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type Retriever = components['schemas']['Retriever'];
export type RetrieverDestination = components['schemas']['RetrieverDestination'];
export type RetrieverHistory = components['schemas']['RetrieverRefreshHistory'];
export type CronPreview = components['schemas']['CronPreviewResult'];
export type CreateRetrieverBody = components['schemas']['CreateRetrieverRequest'];
export type UpdateRetrieverBody = components['schemas']['UpdateRetrieverRequest'];

export class RetrieverRequestError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string
	) {
		super(message);
		this.name = 'RetrieverRequestError';
	}
}

type Result<T> = { data?: T; error?: unknown; response?: Response };

function fail<T>(result: Result<T>, fallback: string): T {
	if (result.error || result.data === undefined) {
		const body = result.error as { code?: string; message?: string } | undefined;
		throw new RetrieverRequestError(
			apiErrorMessage(result.error, fallback),
			result.response?.status,
			body?.code
		);
	}
	return result.data;
}

export async function listRetrievers(workspaceId: string): Promise<Retriever[]> {
	return fail(
		await client.GET('/workspaces/{id}/retrievers', { params: { path: { id: workspaceId } } }),
		'Could not load retrievers'
	);
}

export async function getRetriever(workspaceId: string, id: string): Promise<Retriever> {
	return fail(
		await client.GET('/workspaces/{id}/retrievers/{retriever_id}', {
			params: { path: { id: workspaceId, retriever_id: id } }
		}),
		'Could not load the retriever'
	);
}

export async function createRetriever(
	workspaceId: string,
	body: CreateRetrieverBody
): Promise<Retriever> {
	return fail(
		await client.POST('/workspaces/{id}/retrievers', {
			params: { path: { id: workspaceId } },
			body
		}),
		'Could not create the retriever'
	);
}

export async function updateRetriever(
	workspaceId: string,
	id: string,
	body: UpdateRetrieverBody
): Promise<Retriever> {
	return fail(
		await client.PATCH('/workspaces/{id}/retrievers/{retriever_id}', {
			params: { path: { id: workspaceId, retriever_id: id } },
			body
		}),
		'Could not update the retriever'
	);
}

export async function deleteRetriever(workspaceId: string, id: string): Promise<void> {
	const result = await client.DELETE('/workspaces/{id}/retrievers/{retriever_id}', {
		params: { path: { id: workspaceId, retriever_id: id } }
	});
	if (result.error) fail(result, 'Could not delete the retriever');
}

export async function refreshRetriever(workspaceId: string, id: string): Promise<RetrieverHistory> {
	return fail(
		await client.POST('/workspaces/{id}/retrievers/{retriever_id}/refresh', {
			params: { path: { id: workspaceId, retriever_id: id } },
			body: { refresh_type: 'full' }
		}),
		'Could not start the refresh'
	);
}

export async function listHistory(workspaceId: string, id: string): Promise<RetrieverHistory[]> {
	const result = await client.GET('/workspaces/{id}/retrievers/{retriever_id}/history', {
		params: { path: { id: workspaceId, retriever_id: id } }
	});
	const body = fail(result, 'Could not load the refresh history');
	return body.data ?? [];
}

export async function listDestinations(workspaceId: string): Promise<RetrieverDestination[]> {
	return fail(
		await client.GET('/workspaces/{id}/retrievers/destinations', {
			params: { path: { id: workspaceId } }
		}),
		'Could not load the destinations'
	);
}

export async function validateRetriever(
	workspaceId: string,
	destBindingId: string,
	definitionSql: string
): Promise<void> {
	const result = await client.POST('/workspaces/{id}/retrievers/validate', {
		params: { path: { id: workspaceId } },
		body: { dest_binding_id: destBindingId, definition_sql: definitionSql }
	});
	if (result.error) fail(result, 'The definition is not valid on the destination');
}

export async function previewCron(expression: string): Promise<CronPreview> {
	return fail(
		await client.POST('/cron/preview', { body: { expression } }),
		'Could not preview the schedule'
	);
}
