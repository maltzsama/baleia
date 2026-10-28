// Error catalog (#105 / FD7): every API, engine, lifecycle and auth code maps
// to a plain-language title, explanation and remediation. The UI never shows a
// raw engine message as the headline.
import { m } from '$lib/paraglide/messages';

export type ErrorEntry = {
	title: string;
	explanation: string;
	remediation?: string;
	docsAnchor?: string;
};

export type DescribedError = ErrorEntry & {
	code: string;
	requestId?: string;
	native?: string;
};

export const errorCatalog: Record<string, ErrorEntry> = {
	// API
	unauthorized: {
		get title() {
			return m.errors_catalog_not_signed_in();
		},
		get explanation() {
			return m.errors_catalog_your_session_is_missing_or_has();
		},
		get remediation() {
			return m.errors_catalog_sign_in_again_and_retry();
		}
	},
	forbidden: {
		get title() {
			return m.errors_catalog_not_allowed();
		},
		get explanation() {
			return m.errors_catalog_you_do_not_have_permission_to();
		},
		get remediation() {
			return m.errors_catalog_ask_a_workspace_owner_or_administrator();
		}
	},
	not_found: {
		get title() {
			return m.errors_catalog_not_found();
		},
		get explanation() {
			return m.errors_catalog_the_resource_no_longer_exists_or();
		},
		get remediation() {
			return m.errors_catalog_refresh_the_page_and_check_the();
		}
	},
	conflict: {
		get title() {
			return m.errors_catalog_conflict();
		},
		get explanation() {
			return m.errors_catalog_the_resource_changed_while_you_were();
		},
		get remediation() {
			return m.errors_catalog_reload_the_latest_version_and_apply();
		}
	},
	validation_error: {
		get title() {
			return m.errors_catalog_invalid_input();
		},
		get explanation() {
			return m.errors_catalog_one_or_more_fields_are_invalid();
		},
		get remediation() {
			return m.errors_catalog_fix_the_highlighted_fields_and_retry();
		}
	},
	invalid_definition: {
		get title() {
			return m.errors_catalog_the_query_cannot_be_compiled();
		},
		get explanation() {
			return m.errors_catalog_the_model_or_the_explore_query();
		},
		get remediation() {
			return m.errors_catalog_check_the_highlighted_fields_the_model();
		}
	},
	rate_limited: {
		get title() {
			return m.errors_catalog_too_many_requests();
		},
		get explanation() {
			return m.errors_catalog_you_have_made_too_many_requests();
		},
		get remediation() {
			return m.errors_catalog_wait_for_the_retry_window_before();
		}
	},
	service_unavailable: {
		get title() {
			return m.errors_catalog_service_unavailable();
		},
		get explanation() {
			return m.errors_catalog_a_dependency_is_not_reachable_right();
		},
		get remediation() {
			return m.errors_catalog_retry_in_a_moment_if_it();
		}
	},
	provider_unsupported: {
		get title() {
			return m.errors_catalog_provider_not_supported();
		},
		get explanation() {
			return m.errors_catalog_this_connection_kind_is_not_supported();
		},
		get remediation() {
			return m.errors_catalog_choose_a_different_provider();
		}
	},
	connection_denied_network: {
		get title() {
			return m.errors_catalog_connection_blocked();
		},
		get explanation() {
			return m.errors_catalog_the_server_refused_to_dial_that();
		},
		get remediation() {
			return m.errors_catalog_check_the_host_allow_list_and();
		}
	},
	target_required: {
		get title() {
			return m.errors_catalog_choose_a_target();
		},
		get explanation() {
			return m.errors_catalog_this_workspace_has_more_than_one();
		},
		get remediation() {
			return m.errors_catalog_pick_an_engine_catalog_and_schema();
		}
	},
	engine_required: {
		get title() {
			return m.errors_catalog_choose_an_engine();
		},
		get explanation() {
			return m.errors_catalog_the_selected_catalog_is_not_available();
		},
		get remediation() {
			return m.errors_catalog_pick_another_target_or_enable_an();
		}
	},
	target_unavailable: {
		get title() {
			return m.errors_catalog_target_unavailable();
		},
		get explanation() {
			return m.errors_catalog_the_selected_engine_catalog_or_binding();
		},
		get remediation() {
			return m.errors_catalog_pick_another_target_or_ask_an();
		}
	},
	read_only_unenforceable: {
		get title() {
			return m.errors_catalog_read_only_cannot_be_enforced();
		},
		get explanation() {
			return m.errors_catalog_the_engine_cannot_enforce_read_only();
		},
		get remediation() {
			return m.errors_catalog_grant_write_instead_or_assert_read();
		}
	},
	name_conflict: {
		get title() {
			return m.errors_catalog_name_already_in_use();
		},
		get explanation() {
			return m.errors_catalog_another_item_in_this_workspace_already();
		},
		get remediation() {
			return m.errors_catalog_choose_a_different_name();
		}
	},
	slug_conflict_other_space: {
		get title() {
			return m.errors_catalog_slug_used_in_another_space();
		},
		get explanation() {
			return m.errors_catalog_slugs_are_unique_per_workspace_this();
		},
		get remediation() {
			return m.errors_catalog_rename_the_slug_in_the_bundle();
		}
	},
	property_not_overridable: {
		get title() {
			return m.errors_catalog_property_cannot_be_overridden();
		},
		get explanation() {
			return m.errors_catalog_baleia_sets_this_property_itself();
		},
		get remediation() {
			return m.errors_catalog_remove_the_property_or_change_it();
		}
	},
	invalid_time_range: {
		get title() {
			return m.errors_catalog_invalid_time_range();
		},
		get explanation() {
			return m.errors_catalog_the_selected_window_is_missing_or();
		},
		get remediation() {
			return m.errors_catalog_choose_a_range_of_at_most();
		}
	},
	not_supported: {
		get title() {
			return m.errors_catalog_not_supported();
		},
		get explanation() {
			return m.errors_catalog_this_operation_is_not_available_for();
		},
		get remediation() {
			return m.errors_catalog_choose_a_supported_option();
		}
	},
	version_conflict: {
		get title() {
			return m.errors_catalog_changed_elsewhere();
		},
		get explanation() {
			return m.errors_catalog_someone_else_saved_a_newer_version();
		},
		get remediation() {
			return m.errors_catalog_compare_the_versions_then_overwrite_or();
		}
	},

	// Connection test
	unreachable: {
		get title() {
			return m.errors_catalog_host_unreachable();
		},
		get explanation() {
			return m.errors_catalog_baleia_could_not_reach_the_configured();
		},
		get remediation() {
			return m.errors_catalog_check_the_host_port_and_network();
		}
	},
	tls: {
		get title() {
			return m.errors_catalog_tls_error();
		},
		get explanation() {
			return m.errors_catalog_the_tls_handshake_failed();
		},
		get remediation() {
			return m.errors_catalog_check_the_certificate_and_the_tls();
		}
	},
	auth_failed: {
		get title() {
			return m.errors_catalog_authentication_failed();
		},
		get explanation() {
			return m.errors_catalog_the_provider_rejected_the_supplied_credentials();
		},
		get remediation() {
			return m.errors_catalog_check_the_username_password_or_client();
		}
	},
	timeout: {
		get title() {
			return m.errors_catalog_timed_out();
		},
		get explanation() {
			return m.errors_catalog_the_connection_test_did_not_finish();
		},
		get remediation() {
			return m.errors_catalog_check_the_network_and_retry();
		}
	},
	engine_unavailable: {
		get title() {
			return m.errors_catalog_engine_unavailable();
		},
		get explanation() {
			return m.errors_catalog_the_engine_did_not_answer_the();
		},
		get remediation() {
			return m.errors_catalog_check_engine_health_and_retry();
		}
	},
	internal: {
		get title() {
			return m.errors_catalog_internal_error();
		},
		get explanation() {
			return m.errors_catalog_baleia_hit_an_unexpected_error();
		},
		get remediation() {
			return m.errors_catalog_retry_if_it_persists_check_the();
		}
	},

	// Lifecycle
	plugin_not_confirmed: {
		get title() {
			return m.errors_catalog_catalog_plugin_not_confirmed();
		},
		get explanation() {
			return m.errors_catalog_the_trino_catalog_plugin_has_not();
		},
		get remediation() {
			return m.errors_catalog_wait_for_the_next_reconciliation_or();
		}
	},
	secret_mismatch: {
		get title() {
			return m.errors_catalog_secret_mismatch();
		},
		get explanation() {
			return m.errors_catalog_the_stored_secret_does_not_match();
		},
		get remediation() {
			return m.errors_catalog_update_the_credentials_on_the_connection();
		}
	},
	credentials_unreadable: {
		get title() {
			return m.errors_catalog_credentials_unreadable();
		},
		get explanation() {
			return m.errors_catalog_baleia_cannot_decrypt_the_stored_credentials();
		},
		get remediation() {
			return m.errors_catalog_re_enter_the_credentials_the_encryption();
		}
	},
	version_mismatch: {
		get title() {
			return m.errors_catalog_version_mismatch();
		},
		get explanation() {
			return m.errors_catalog_the_installed_component_version_does_not();
		},
		get remediation() {
			return m.errors_catalog_upgrade_the_component_or_update_the();
		}
	},

	// Auth
	invalid_credentials: {
		get title() {
			return m.errors_catalog_invalid_credentials();
		},
		get explanation() {
			return m.errors_catalog_the_e_mail_and_password_do();
		},
		get remediation() {
			return m.errors_catalog_check_the_values_and_try_again();
		}
	},
	not_provisioned: {
		get title() {
			return m.errors_catalog_account_not_provisioned();
		},
		get explanation() {
			return m.errors_catalog_your_identity_provider_authenticated_you_but();
		},
		get remediation() {
			return m.errors_catalog_ask_an_administrator_to_invite_you();
		}
	},
	account_suspended: {
		get title() {
			return m.errors_catalog_account_suspended();
		},
		get explanation() {
			return m.errors_catalog_an_administrator_suspended_this_account();
		},
		get remediation() {
			return m.errors_catalog_contact_an_administrator_to_reinstate_it();
		}
	},
	idp_unavailable: {
		get title() {
			return m.errors_catalog_identity_provider_unavailable();
		},
		get explanation() {
			return m.errors_catalog_the_identity_provider_did_not_answer();
		},
		get remediation() {
			return m.errors_catalog_retry_if_it_persists_contact_your();
		}
	},
	sso_failed: {
		get title() {
			return m.errors_catalog_single_sign_on_failed();
		},
		get explanation() {
			return m.errors_catalog_the_identity_provider_could_not_complete();
		},
		get remediation() {
			return m.errors_catalog_try_again_if_it_persists_contact();
		}
	},
	network_error: {
		get title() {
			return m.errors_catalog_connection_error();
		},
		get explanation() {
			return m.errors_catalog_baleia_could_not_reach_the_server();
		},
		get remediation() {
			return m.errors_catalog_check_that_the_server_is_running();
		}
	},
	agent_token_not_allowed: {
		get title() {
			return m.errors_catalog_agent_token_not_allowed();
		},
		get explanation() {
			return m.errors_catalog_this_token_cannot_be_used_for();
		},
		get remediation() {
			return m.errors_catalog_use_a_human_session_or_an();
		}
	},

	// Agent gate reasons
	policy: {
		get title() {
			return m.errors_catalog_blocked_by_policy();
		},
		get explanation() {
			return m.errors_catalog_the_agent_capability_policy_denies_this();
		},
		get remediation() {
			return m.errors_catalog_ask_the_agent_owner_to_widen();
		}
	},
	scope: {
		get title() {
			return m.errors_catalog_outside_the_agent_scope();
		},
		get explanation() {
			return m.errors_catalog_the_action_is_outside_what_this();
		},
		get remediation() {
			return m.errors_catalog_use_a_narrower_target_or_a();
		}
	},
	rate: {
		get title() {
			return m.errors_catalog_agent_rate_limited();
		},
		get explanation() {
			return m.errors_catalog_the_agent_exceeded_its_request_budget();
		},
		get remediation() {
			return m.errors_catalog_wait_for_the_window_to_reset();
		}
	},
	quota: {
		get title() {
			return m.errors_catalog_agent_quota_reached();
		},
		get explanation() {
			return m.errors_catalog_the_agent_reached_its_usage_quota();
		},
		get remediation() {
			return m.errors_catalog_raise_the_quota_or_wait_for();
		}
	},
	budget: {
		get title() {
			return m.errors_catalog_agent_budget_reached();
		},
		get explanation() {
			return m.errors_catalog_the_agent_reached_its_configured_budget();
		},
		get remediation() {
			return m.errors_catalog_raise_the_budget_in_the_agent();
		}
	},
	statement: {
		get title() {
			return m.errors_catalog_statement_rejected();
		},
		get explanation() {
			return m.errors_catalog_the_statement_is_not_allowed_for();
		},
		get remediation() {
			return m.errors_catalog_use_a_read_only_statement();
		}
	},

	// Attention center (#268). The catalog key is `attention.<kind>`.
	'attention.binding_error': {
		get title() {
			return m.errors_catalog_catalog_binding_error();
		},
		get explanation() {
			return m.errors_catalog_a_catalog_binding_needs_attention();
		},
		get remediation() {
			return m.errors_catalog_open_the_catalog_bindings_tab_and();
		}
	},
	'attention.metadata_sync_failed': {
		get title() {
			return m.errors_catalog_metadata_sync_failed();
		},
		get explanation() {
			return m.errors_catalog_the_last_metadata_sync_for_this();
		},
		get remediation() {
			return m.errors_catalog_open_the_catalog_and_retry_the();
		}
	},
	'attention.metadata_stale': {
		get title() {
			return m.errors_catalog_metadata_is_stale();
		},
		get explanation() {
			return m.errors_catalog_this_catalog_has_not_synced_within();
		},
		get remediation() {
			return m.errors_catalog_open_the_catalog_and_run_a();
		}
	},
	'attention.engine_unreachable': {
		get title() {
			return m.errors_catalog_engine_unreachable();
		},
		get explanation() {
			return m.errors_catalog_baleia_cannot_reach_this_engine();
		},
		get remediation() {
			return m.errors_catalog_check_the_engine_health_and_credentials();
		}
	},
	'attention.retriever_error': {
		get title() {
			return m.errors_catalog_retriever_failed();
		},
		get explanation() {
			return m.errors_catalog_this_retriever_stopped_with_an_error();
		},
		get remediation() {
			return m.errors_catalog_open_the_retriever_and_review_the();
		}
	},
	'attention.reconcile_drift': {
		get title() {
			return m.errors_catalog_catalog_drift_detected();
		},
		get explanation() {
			return m.errors_catalog_the_last_reconcile_found_changes_that();
		},
		get remediation() {
			return m.errors_catalog_open_the_engine_reconcile_tab_and();
		}
	},
	'attention.schedule_missing': {
		get title() {
			return m.errors_catalog_scheduled_job_missing();
		},
		get explanation() {
			return m.errors_catalog_a_recurring_temporal_schedule_is_missing();
		},
		get remediation() {
			return m.errors_catalog_open_the_system_status_page_and();
		}
	},
	'attention.audit_export_lagging': {
		get title() {
			return m.errors_catalog_audit_export_is_lagging();
		},
		get explanation() {
			return m.errors_catalog_audit_events_are_not_reaching_the();
		},
		get remediation() {
			return m.errors_catalog_open_the_system_status_page_and_2();
		}
	},
	'attention.agent_token_expiring': {
		get title() {
			return m.errors_catalog_agent_token_expiring();
		},
		get explanation() {
			return m.errors_catalog_an_agent_token_expires_within_seven();
		},
		get remediation() {
			return m.errors_catalog_open_the_agent_and_rotate_its();
		}
	},

	// A statement the engine ran (#393). The catalog key is `statement.<code>`,
	// the engine code the worker stores; the engine's own message is the
	// detail that names the object or the position.
	'statement.syntax': {
		get title() {
			return m.errors_catalog_sql_error();
		},
		get explanation() {
			return m.errors_catalog_the_engine_could_not_parse_or();
		},
		get remediation() {
			return m.errors_catalog_fix_the_statement_at_the_line();
		}
	},
	'statement.not_found': {
		get title() {
			return m.errors_catalog_name_not_found();
		},
		get explanation() {
			return m.errors_catalog_a_catalog_schema_table_column_or();
		},
		get remediation() {
			return m.errors_catalog_check_the_spelling_and_the_qualified();
		}
	},
	'statement.permission_denied': {
		get title() {
			return m.errors_catalog_access_denied_by_the_engine();
		},
		get explanation() {
			return m.errors_catalog_the_engine_refused_the_statement_for();
		},
		get remediation() {
			return m.errors_catalog_ask_an_administrator_for_access_to();
		}
	},
	'statement.read_only_unsupported': {
		get title() {
			return m.errors_catalog_writes_are_not_allowed_here();
		},
		get explanation() {
			return m.errors_catalog_this_target_runs_read_only_and();
		},
		get remediation() {
			return m.errors_catalog_run_a_read_only_statement_or();
		}
	},
	'statement.timeout': {
		get title() {
			return m.errors_catalog_statement_timed_out();
		},
		get explanation() {
			return m.errors_catalog_the_engine_stopped_the_statement_at();
		},
		get remediation() {
			return m.errors_catalog_narrow_the_query_filters_limit_or();
		}
	},
	'statement.cancelled': {
		get title() {
			return m.errors_catalog_statement_cancelled();
		},
		get explanation() {
			return m.errors_catalog_the_statement_was_cancelled_before_it();
		},
		get remediation() {
			return m.errors_catalog_run_it_again_if_you_still();
		}
	},
	'statement.resource_exhausted': {
		get title() {
			return m.errors_catalog_engine_out_of_resources();
		},
		get explanation() {
			return m.errors_catalog_the_engine_ran_out_of_memory();
		},
		get remediation() {
			return m.errors_catalog_retry_later_or_narrow_the_query();
		}
	},
	'statement.engine_unavailable': {
		get title() {
			return m.errors_catalog_engine_unavailable();
		},
		get explanation() {
			return m.errors_catalog_the_engine_could_not_be_reached();
		},
		get remediation() {
			return m.errors_catalog_retry_in_a_moment_if_it_2();
		}
	},
	'statement.conflict': {
		get title() {
			return m.errors_catalog_write_conflict();
		},
		get explanation() {
			return m.errors_catalog_the_statement_conflicted_with_a_concurrent();
		},
		get remediation() {
			return m.errors_catalog_run_it_again_it_is_never();
		}
	},
	'statement.interrupted': {
		get title() {
			return m.errors_catalog_run_interrupted();
		},
		get explanation() {
			return m.errors_catalog_the_baleia_worker_stopped_while_the();
		},
		get remediation() {
			return m.errors_catalog_check_the_data_then_re_run();
		}
	},
	'statement.internal': {
		get title() {
			return m.errors_catalog_statement_failed();
		},
		get explanation() {
			return m.errors_catalog_the_engine_reported_an_error_baleia();
		},
		get remediation() {
			return m.errors_catalog_read_the_engine_message_below();
		}
	}
};

/**
 * Describes an error for the UI. With `scope`, a `<scope>.<code>` entry wins
 * over the plain code: a statement's `not_found` is a missing table, not a
 * deleted resource.
 */
export function describeError(error: unknown, scope?: 'statement'): DescribedError {
	let code = 'internal';
	let requestId: string | undefined;
	let native: string | undefined;

	if (typeof error === 'string') {
		code = error;
	} else if (error && typeof error === 'object') {
		const value = error as Record<string, unknown>;
		if (typeof value.code === 'string') code = value.code;
		if (typeof value.request_id === 'string') requestId = value.request_id;
		if (typeof value.error_native === 'string') native = value.error_native;
	}

	const entry = (scope && errorCatalog[`${scope}.${code}`]) ||
		errorCatalog[code] || {
			title: m.errors_catalog_unexpected_error(),
			explanation: m.errors_catalog_baleia_could_not_classify_this_error(),
			remediation: m.errors_catalog_retry_if_it_persists_contact_an()
		};

	return { code, ...entry, requestId, native };
}
