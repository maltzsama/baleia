// The guarantees panel (#110) renders capabilities as consequences, never as
// raw booleans. Kept pure so every engine kind can be table-tested.
import { m } from '$lib/paraglide/messages';
import { readOnlyGuarantee, type Engine } from './admin';

export type Guarantee = {
	title: string;
	body: string;
	tone: 'neutral' | 'info' | 'success' | 'warning' | 'danger';
};

export function describeGuarantees(e: Engine): Guarantee[] {
	const out: Guarantee[] = [];
	const ro = readOnlyGuarantee(e);
	out.push({ title: m.engines_guarantees_read_only(), body: ro.detail, tone: ro.tone });
	const c = e.capabilities;
	if (!c) {
		out.push({
			title: m.engines_guarantees_capabilities(),
			body: m.engines_guarantees_the_adapter_is_unavailable_disabled_engine(),
			tone: 'warning'
		});
		return out;
	}
	if (e.identity_mode === 'impersonation') {
		out.push({
			title: m.engines_guarantees_identity(),
			body: m.engines_guarantees_queries_run_as_each_baleia_user(),
			tone: 'success'
		});
	} else if (e.identity_mode === 'connection') {
		out.push({
			title: m.engines_guarantees_identity(),
			body: m.engines_guarantees_queries_run_as_the_connection_s(),
			tone: 'warning'
		});
	} else {
		out.push({
			title: m.engines_guarantees_identity(),
			body: m.engines_guarantees_per_user_identity_is_not_available(),
			tone: 'neutral'
		});
	}
	if (c.transactions) {
		out.push({
			title: m.engines_guarantees_transactions(),
			body: m.engines_guarantees_statements_of_one_run_share_a(),
			tone: 'success'
		});
	} else {
		out.push({
			title: m.engines_guarantees_transactions(),
			body: m.engines_guarantees_no_transactions(),
			tone: 'neutral'
		});
	}
	if (c.supports_cancel) {
		out.push({
			title: m.engines_guarantees_cancel(),
			body:
				c.cancel_scope === 'any_process'
					? m.engines_guarantees_cancel_works_even_if_the_worker()
					: m.engines_guarantees_cancel_works_while_the_run_is(),
			tone: c.cancel_scope === 'any_process' ? 'success' : 'warning'
		});
	} else {
		out.push({
			title: m.engines_guarantees_cancel(),
			body: m.engines_guarantees_cancel_is_not_supported(),
			tone: 'warning'
		});
	}
	if (!c.session_schema) {
		out.push({
			title: m.engines_guarantees_session_schema(),
			body: m.engines_guarantees_default_schema_cannot_be_set_per(),
			tone: 'warning'
		});
	}
	if (c.namespace_depth === 0) {
		out.push({
			title: m.engines_guarantees_namespaces(),
			body: m.engines_guarantees_no_schemas_tables_are_addressed_by(),
			tone: 'neutral'
		});
	}
	out.push({
		title: m.engines_guarantees_catalogs(),
		body: c.multiple_catalogs
			? m.engines_guarantees_can_join_across_catalogs_of_this()
			: m.engines_guarantees_one_catalog_per_query(),
		tone: c.multiple_catalogs ? 'success' : 'neutral'
	});
	out.push({
		title: m.engines_guarantees_identifiers(),
		body: m.engines_guarantees_unquoted_identifiers_are_folded_to_case({
			value: c.identifier_case.toLowerCase(),
			max_identifier_length: c.max_identifier_length
		}),
		tone: 'neutral'
	});
	if (c.supports_time_travel)
		out.push({
			title: m.engines_guarantees_time_travel(),
			body: m.engines_guarantees_queries_can_read_a_table_as(),
			tone: 'success'
		});
	if (c.monitoring)
		out.push({
			title: m.engines_guarantees_monitoring(),
			body: m.engines_guarantees_baleia_can_show_cluster_and_query(),
			tone: 'success'
		});
	if (c.iceberg_maintenance)
		out.push({
			title: m.engines_guarantees_iceberg_maintenance(),
			body: m.engines_guarantees_baleia_can_optimize_tables_and_expire(),
			tone: 'success'
		});
	if (c.arrow_native)
		out.push({
			title: m.engines_guarantees_arrow(),
			body: m.engines_guarantees_results_stream_natively_as_arrow(),
			tone: 'success'
		});
	if (c.supports_explain)
		out.push({
			title: m.engines_guarantees_explain(),
			body: m.engines_guarantees_explain_is_available_from_the_editor(),
			tone: 'success'
		});
	if (e.dialect) {
		out.push({
			title: m.engines_guarantees_dialect(),
			body: m.engines_guarantees_the_editor_uses_formatting({
				name: e.dialect.name,
				formatter_language: e.dialect.formatter_language
			}),
			tone: 'neutral'
		});
	}
	return out;
}
