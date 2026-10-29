// Readable names for maintenance operations and plan reasons (#500).
import { m } from '$lib/paraglide/messages';
import type { MaintenancePlanStep } from './client';

export function operationLabel(op: string): string {
	switch (op) {
		case 'optimize':
			return m.maintenance_op_optimize();
		case 'optimize_manifests':
			return m.maintenance_op_optimize_manifests();
		case 'expire_snapshots':
			return m.maintenance_op_expire_snapshots();
		case 'remove_orphan_files':
			return m.maintenance_op_remove_orphan_files();
	}
	return op;
}

export function triggerLabel(trigger: string): string {
	switch (trigger) {
		case 'automatic':
			return m.maintenance_trigger_automatic();
		case 'dry_run':
			return m.maintenance_trigger_dry_run();
	}
	return m.maintenance_trigger_manual();
}

function firedLabel(fired: string): string {
	switch (fired) {
		case 'small_file_ratio':
			return m.maintenance_fired_small_files();
		case 'delete_files':
			return m.maintenance_fired_delete_files();
		case 'manifests':
			return m.maintenance_fired_manifests();
		case 'snapshots':
			return m.maintenance_fired_snapshots();
		case 'oldest_age_seconds':
			return m.maintenance_fired_oldest_age();
		case 'max_interval':
			return m.maintenance_fired_max_interval();
		case 'manual':
			return m.maintenance_fired_manual();
	}
	return fired;
}

const value = (n: number | undefined, fired: string | undefined) =>
	n === undefined ? '' : fired === 'small_file_ratio' ? `${Math.round(n * 100)}%` : String(n);

/** What a plan step means: why it runs, waits, or is not due. */
export function reasonText(step: MaintenancePlanStep): string {
	const r = step.reason;
	if (step.due) {
		const why =
			r.measured !== undefined && r.threshold !== undefined
				? m.maintenance_reason_measured({
						trigger: firedLabel(r.fired ?? ''),
						measured: value(r.measured, r.fired),
						threshold: value(r.threshold, r.fired)
					})
				: firedLabel(r.fired ?? '');
		if (step.would_run) return m.maintenance_reason_runs_next({ why });
		return m.maintenance_reason_waits({ why, operation: operationLabel(step.waits_for ?? '') });
	}
	switch (r.blocked) {
		case 'disabled':
			return m.maintenance_blocked_disabled();
		case 'mode':
			return m.maintenance_blocked_mode();
		case 'min_interval':
			return m.maintenance_blocked_min_interval();
		case 'window':
			return m.maintenance_blocked_window();
		case 'running':
			return m.maintenance_blocked_running();
	}
	return m.maintenance_blocked_not_fired();
}
