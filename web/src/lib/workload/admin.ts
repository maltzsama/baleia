// Workload administration calls (#547): the ordered rules and the per-engine
// admission queues. All of it lives behind `manage_workload` on /auth/me.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type WorkloadRule = components['schemas']['WorkloadRule'];
export type WorkloadRuleMatch = components['schemas']['WorkloadRuleMatch'];
export type CreateWorkloadRuleRequest = components['schemas']['CreateWorkloadRuleRequest'];
export type UpdateWorkloadRuleRequest = components['schemas']['UpdateWorkloadRuleRequest'];
export type WorkloadRuleTestRequest = components['schemas']['WorkloadRuleTestRequest'];
export type WorkloadRuleTestResult = components['schemas']['WorkloadRuleTestResult'];
export type EngineQueues = components['schemas']['EngineQueues'];
export type EngineQueue = components['schemas']['EngineQueue'];
export type UpdateEngineQueuesRequest = components['schemas']['UpdateEngineQueuesRequest'];

type Result<T> = { data?: T; error?: unknown; response?: Response };

export class WorkloadAdminError extends Error {
	constructor(
		message: string,
		readonly status?: number,
		readonly code?: string
	) {
		super(message);
		this.name = 'WorkloadAdminError';
	}
}

function fail<T>(result: Result<T>, fallback: string): T {
	if (result.error || result.data === undefined) {
		const body = result.error as { code?: string } | undefined;
		throw new WorkloadAdminError(
			apiErrorMessage(result.error, fallback),
			result.response?.status,
			body?.code
		);
	}
	return result.data;
}

export async function listWorkloadRules(): Promise<WorkloadRule[]> {
	return fail(await client.GET('/workload-rules'), 'Could not load the workload rules');
}

export async function createWorkloadRule(body: CreateWorkloadRuleRequest): Promise<WorkloadRule> {
	return fail(await client.POST('/workload-rules', { body }), 'Could not create the rule');
}

export async function updateWorkloadRule(
	id: string,
	body: UpdateWorkloadRuleRequest
): Promise<WorkloadRule> {
	return fail(
		await client.PATCH('/workload-rules/{rule_id}', { params: { path: { rule_id: id } }, body }),
		'Could not update the rule'
	);
}

export async function deleteWorkloadRule(id: string): Promise<void> {
	const result = await client.DELETE('/workload-rules/{rule_id}', {
		params: { path: { rule_id: id } }
	});
	if (result.error) fail(result, 'Could not delete the rule');
}

export async function testWorkloadRule(
	body: WorkloadRuleTestRequest
): Promise<WorkloadRuleTestResult> {
	return fail(await client.POST('/workload-rules:test', { body }), 'Could not test the rule');
}

export async function getEngineQueues(id: string): Promise<EngineQueues> {
	return fail(
		await client.GET('/engines/{id}/queues', { params: { path: { id } } }),
		'Could not load the queues'
	);
}

export async function putEngineQueues(
	id: string,
	body: UpdateEngineQueuesRequest
): Promise<EngineQueues> {
	return fail(
		await client.PUT('/engines/{id}/queues', { params: { path: { id } }, body }),
		'Could not save the capacities'
	);
}
