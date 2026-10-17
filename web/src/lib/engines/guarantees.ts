// The guarantees panel (#110) renders capabilities as consequences, never as
// raw booleans. Kept pure so every engine kind can be table-tested.
import { readOnlyGuarantee, type Engine } from './admin';

export type Guarantee = {
	title: string;
	body: string;
	tone: 'neutral' | 'info' | 'success' | 'warning' | 'danger';
};

export function describeGuarantees(e: Engine): Guarantee[] {
	const out: Guarantee[] = [];
	const ro = readOnlyGuarantee(e);
	out.push({ title: 'Read-only', body: ro.detail, tone: ro.tone });
	const c = e.capabilities;
	if (!c) {
		out.push({
			title: 'Capabilities',
			body: 'The adapter is unavailable (disabled engine or an unsupported kind), so Baleia cannot state its guarantees.',
			tone: 'warning'
		});
		return out;
	}
	if (e.identity_mode === 'impersonation') {
		out.push({
			title: 'Identity',
			body: 'Queries run as each Baleia user (impersonation).',
			tone: 'success'
		});
	} else if (e.identity_mode === 'connection') {
		out.push({
			title: 'Identity',
			body: "Queries run as the connection's user for everyone.",
			tone: 'warning'
		});
	} else {
		out.push({
			title: 'Identity',
			body: 'Per-user identity is not available in v1.',
			tone: 'neutral'
		});
	}
	if (c.transactions) {
		out.push({
			title: 'Transactions',
			body: 'Statements of one run share a session; BEGIN … COMMIT works inside a run. A run is not a transaction unless you write one.',
			tone: 'success'
		});
	} else {
		out.push({ title: 'Transactions', body: 'No transactions.', tone: 'neutral' });
	}
	if (c.supports_cancel) {
		out.push({
			title: 'Cancel',
			body:
				c.cancel_scope === 'any_process'
					? 'Cancel works even if the worker running the query restarts.'
					: 'Cancel works while the run is active; a query orphaned by a worker crash cannot be stopped from Baleia.',
			tone: c.cancel_scope === 'any_process' ? 'success' : 'warning'
		});
	} else {
		out.push({ title: 'Cancel', body: 'Cancel is not supported.', tone: 'warning' });
	}
	if (!c.session_schema) {
		out.push({
			title: 'Session schema',
			body: "Default schema cannot be set per session; unqualified names resolve with the login's default schema.",
			tone: 'warning'
		});
	}
	if (c.namespace_depth === 0) {
		out.push({
			title: 'Namespaces',
			body: 'No schemas — tables are addressed by name only.',
			tone: 'neutral'
		});
	}
	out.push({
		title: 'Catalogs',
		body: c.multiple_catalogs
			? 'Can join across catalogs of this engine in one query.'
			: 'One catalog per query.',
		tone: c.multiple_catalogs ? 'success' : 'neutral'
	});
	out.push({
		title: 'Identifiers',
		body: `Unquoted identifiers are folded to ${c.identifier_case.toLowerCase()} case; max ${c.max_identifier_length} characters.`,
		tone: 'neutral'
	});
	if (c.supports_time_travel)
		out.push({
			title: 'Time travel',
			body: 'Queries can read a table as of an earlier time.',
			tone: 'success'
		});
	if (c.monitoring)
		out.push({
			title: 'Monitoring',
			body: 'Baleia can show cluster and query activity for this engine.',
			tone: 'success'
		});
	if (c.iceberg_maintenance)
		out.push({
			title: 'Iceberg maintenance',
			body: 'Baleia can optimize tables and expire snapshots.',
			tone: 'success'
		});
	if (c.arrow_native)
		out.push({ title: 'Arrow', body: 'Results stream natively as Arrow.', tone: 'success' });
	if (c.supports_explain)
		out.push({ title: 'Explain', body: 'EXPLAIN is available from the editor.', tone: 'success' });
	if (e.dialect) {
		out.push({
			title: 'Dialect',
			body: `${e.dialect.name}; the editor uses ${e.dialect.formatter_language} formatting.`,
			tone: 'neutral'
		});
	}
	return out;
}
