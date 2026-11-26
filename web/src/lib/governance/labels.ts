// Localized labels of the stable codes the data policy API returns (FD7): the
// screens never show a raw code as the headline.
import { m } from '$lib/paraglide/messages';
import type { DataPolicyProblem, DataPolicyRevision } from './api';

const invalidReasons: Record<string, () => string> = {
	empty: m.governance_invalid_empty,
	engine_refused: m.governance_invalid_engine_refused,
	function_not_allowed: m.governance_invalid_function_not_allowed,
	not_allowed: m.governance_invalid_not_allowed,
	not_an_expression: m.governance_invalid_not_an_expression,
	not_boolean: m.governance_invalid_not_boolean,
	qualified_column: m.governance_invalid_qualified_column,
	syntax: m.governance_invalid_syntax,
	too_long: m.governance_invalid_too_long,
	type_mismatch: m.governance_invalid_type_mismatch,
	unknown_column: m.governance_invalid_unknown_column
};

/** Why a policy's expression is invalid. */
export function invalidReasonLabel(reason: string | null | undefined): string {
	return (reason && invalidReasons[reason]?.()) || m.governance_invalid_other();
}

/** One reason a publish or rollback was refused. `policyName` names the policy it is about. */
export function problemLabel(problem: DataPolicyProblem, policyName?: string): string {
	const detail = problem.detail ?? '';
	const policy = policyName ?? problem.policy_id ?? '';
	switch (problem.code) {
		case 'invalid':
			return m.governance_problem_invalid({ policy, reason: invalidReasonLabel(detail) });
		case 'not_checked':
			return m.governance_problem_not_checked({ policy });
		case 'duplicate_priority':
			return m.governance_problem_duplicate_priority({ policy, detail });
		case 'catalog_name_conflict':
			return m.governance_problem_catalog_name_conflict({ detail });
		case 'trusted_definer_view':
			return m.governance_problem_trusted_definer_view({ detail });
		case 'views_not_inspectable':
			return m.governance_problem_views_not_inspectable({ detail });
		default:
			return m.governance_problem_other();
	}
}

const healthReasons: Record<string, () => string> = {
	no_report: m.governance_health_no_report,
	report_stale: m.governance_health_report_stale,
	bundle_error: m.governance_health_bundle_error,
	no_active_revision: m.governance_health_no_active_revision,
	unknown_revision: m.governance_health_unknown_revision,
	lease_expired: m.governance_health_lease_expired,
	data_policy_digest_mismatch: m.governance_health_digest_mismatch
};

/** Why an engine's OPA integration is unhealthy. */
export function healthReasonLabel(reason: string | null | undefined): string {
	return (reason && healthReasons[reason]?.()) || m.governance_health_other();
}

/** Why a revision was built. */
export function revisionReasonLabel(reason: DataPolicyRevision['reason']): string {
	switch (reason) {
		case 'publish':
			return m.governance_revision_publish();
		case 'reissue':
			return m.governance_revision_reissue();
		case 'rollback':
			return m.governance_revision_rollback();
		case 'subjects':
			return m.governance_revision_subjects();
	}
}
