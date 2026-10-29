// The table maintenance form (#500) works in readable units: intervals and
// timeouts in minutes, retentions and ages in hours, sizes in MB and the
// small-file ratio in percent. These helpers convert to and from the API's
// seconds, bytes and ratios, and name each form field by its API path so a
// field error lands on its input.
import type {
	MaintenanceMode,
	MaintenanceOperation,
	MaintenancePolicy,
	MaintenanceSettings
} from './client';

export type OperationForm = {
	enabled: boolean;
	// triggers
	smallFilePercent: string;
	deleteFiles: string;
	manifests: string;
	snapshots: string;
	oldestAgeHours: string;
	// schedule
	minIntervalMinutes: string;
	maxIntervalMinutes: string;
	windowEnabled: boolean;
	startHour: string;
	endHour: string;
	timeZone: string;
	timeoutMinutes: string;
	onConflict: 'retry_with_backoff' | 'give_up_and_report';
	// params
	targetFileSizeMB: string;
	partitionFilter: string;
	skipFilesNewerThanMinutes: string;
	sortedBy: string;
	retentionHours: string;
	retainLast: string;
	cleanExpiredMetadata: boolean;
	olderThanHours: string;
};

/** The form fields that hold text inputs. */
export type TextKey = {
	[K in keyof OperationForm]: OperationForm[K] extends string
		? K extends 'onConflict'
			? never
			: K
		: never;
}[keyof OperationForm];

export type MaintenanceForm = {
	mode: MaintenanceMode;
	writerCommitIntervalSeconds: string;
	operations: Record<MaintenanceOperation, OperationForm>;
};

const MB = 1024 * 1024;

const text = (v: number | null | undefined, divisor = 1): string =>
	v === null || v === undefined ? '' : String(Math.round((v / divisor) * 100) / 100);

function num(value: string, multiplier = 1): number | null {
	const trimmed = value.trim();
	if (trimmed === '') return null;
	const n = Number(trimmed);
	return Number.isFinite(n) ? Math.round(n * multiplier) : NaN;
}

function operationForm(s: MaintenanceSettings): OperationForm {
	const t = s.trigger;
	const p = s.params;
	return {
		enabled: s.enabled,
		smallFilePercent:
			t.small_file_ratio == null ? '' : String(Math.round(t.small_file_ratio * 100)),
		deleteFiles: text(t.delete_files),
		manifests: text(t.manifests),
		snapshots: text(t.snapshots),
		oldestAgeHours: text(t.oldest_age_seconds, 3600),
		minIntervalMinutes: text(s.min_interval_seconds, 60),
		maxIntervalMinutes: text(s.max_interval_seconds, 60),
		windowEnabled: !!s.window,
		startHour: s.window ? String(s.window.start_hour) : '0',
		endHour: s.window ? String(s.window.end_hour) : '23',
		timeZone: s.window?.tz ?? 'UTC',
		timeoutMinutes: text(s.timeout_seconds, 60),
		onConflict: s.on_conflict,
		targetFileSizeMB: text(p.target_file_size_bytes, MB),
		partitionFilter: p.partition_filter ?? '',
		skipFilesNewerThanMinutes: text(p.skip_files_newer_than_seconds, 60),
		sortedBy: (p.sorted_by ?? []).join(', '),
		retentionHours: text(p.retention_seconds, 3600),
		retainLast: text(p.retain_last),
		cleanExpiredMetadata: p.clean_expired_metadata ?? false,
		olderThanHours: text(p.older_than_seconds, 3600)
	};
}

export function toForm(policy: MaintenancePolicy): MaintenanceForm {
	return {
		mode: policy.mode,
		writerCommitIntervalSeconds: String(policy.writer_commit_interval_seconds),
		operations: {
			optimize: operationForm(policy.optimize),
			optimize_manifests: operationForm(policy.optimize_manifests),
			expire_snapshots: operationForm(policy.expire_snapshots),
			remove_orphan_files: operationForm(policy.remove_orphan_files)
		}
	};
}

// Each operation sends only its own triggers and params: the server refuses
// another operation's fields.
function settings(op: MaintenanceOperation, f: OperationForm): MaintenanceSettings {
	const s: MaintenanceSettings = {
		enabled: f.enabled,
		trigger: {},
		min_interval_seconds: num(f.minIntervalMinutes, 60) ?? 0,
		max_interval_seconds: num(f.maxIntervalMinutes, 60),
		window: f.windowEnabled
			? { start_hour: num(f.startHour) ?? 0, end_hour: num(f.endHour) ?? 23, tz: f.timeZone.trim() }
			: null,
		timeout_seconds: num(f.timeoutMinutes, 60) ?? 0,
		on_conflict: f.onConflict,
		params: {}
	};
	switch (op) {
		case 'optimize': {
			const ratio = num(f.smallFilePercent);
			s.trigger = {
				small_file_ratio: ratio === null ? null : ratio / 100,
				delete_files: num(f.deleteFiles)
			};
			const sorted = f.sortedBy
				.split(',')
				.map((c) => c.trim())
				.filter(Boolean);
			s.params = {
				target_file_size_bytes: num(f.targetFileSizeMB, MB),
				partition_filter: f.partitionFilter.trim() || null,
				skip_files_newer_than_seconds: num(f.skipFilesNewerThanMinutes, 60),
				sorted_by: sorted.length ? sorted : null
			};
			break;
		}
		case 'optimize_manifests':
			s.trigger = { manifests: num(f.manifests) };
			break;
		case 'expire_snapshots':
			s.trigger = { snapshots: num(f.snapshots), oldest_age_seconds: num(f.oldestAgeHours, 3600) };
			s.params = {
				retention_seconds: num(f.retentionHours, 3600),
				retain_last: num(f.retainLast),
				clean_expired_metadata: f.cleanExpiredMetadata
			};
			break;
		case 'remove_orphan_files':
			s.params = { older_than_seconds: num(f.olderThanHours, 3600) };
			break;
	}
	return s;
}

export function fromForm(form: MaintenanceForm): MaintenancePolicy {
	return {
		mode: form.mode,
		writer_commit_interval_seconds: num(form.writerCommitIntervalSeconds) ?? 0,
		optimize: settings('optimize', form.operations.optimize),
		optimize_manifests: settings('optimize_manifests', form.operations.optimize_manifests),
		expire_snapshots: settings('expire_snapshots', form.operations.expire_snapshots),
		remove_orphan_files: settings('remove_orphan_files', form.operations.remove_orphan_files)
	};
}

/**
 * New parameters start from what the table already declares: Iceberg's own
 * expiry age, snapshots to keep and target file size.
 */
export function fromTableProperties(
	policy: MaintenancePolicy,
	properties: Record<string, string> | undefined
): MaintenancePolicy {
	if (!properties) return policy;
	const out = structuredClone(policy);
	const age = Number(properties['history.expire.max-snapshot-age-ms']);
	if (Number.isFinite(age) && age > 0)
		out.expire_snapshots.params.retention_seconds = Math.round(age / 1000);
	const keep = Number(properties['history.expire.min-snapshots-to-keep']);
	if (Number.isFinite(keep) && keep > 0) out.expire_snapshots.params.retain_last = keep;
	const target = Number(properties['write.target-file-size-bytes']);
	if (Number.isFinite(target) && target > 0) out.optimize.params.target_file_size_bytes = target;
	return out;
}

/** The Iceberg property a form field starts from, when the table declares it. */
export const PROPERTY_OF: Partial<Record<string, string>> = {
	'expire_snapshots.retentionHours': 'history.expire.max-snapshot-age-ms',
	'expire_snapshots.retainLast': 'history.expire.min-snapshots-to-keep',
	'optimize.targetFileSizeMB': 'write.target-file-size-bytes'
};

// API path → form key, for the fields whose names differ.
const FORM_KEY: Record<string, keyof OperationForm> = {
	'trigger.small_file_ratio': 'smallFilePercent',
	'trigger.delete_files': 'deleteFiles',
	'trigger.manifests': 'manifests',
	'trigger.snapshots': 'snapshots',
	'trigger.oldest_age_seconds': 'oldestAgeHours',
	min_interval_seconds: 'minIntervalMinutes',
	max_interval_seconds: 'maxIntervalMinutes',
	'window.start_hour': 'startHour',
	'window.end_hour': 'endHour',
	'window.tz': 'timeZone',
	timeout_seconds: 'timeoutMinutes',
	on_conflict: 'onConflict',
	'params.target_file_size_bytes': 'targetFileSizeMB',
	'params.partition_filter': 'partitionFilter',
	'params.skip_files_newer_than_seconds': 'skipFilesNewerThanMinutes',
	'params.sorted_by': 'sortedBy',
	'params.retention_seconds': 'retentionHours',
	'params.retain_last': 'retainLast',
	'params.older_than_seconds': 'olderThanHours'
};

/**
 * The form key of an API field path: "expire_snapshots.params.retain_last"
 * is "expire_snapshots.retainLast"; top-level fields keep their name.
 */
export function formKey(field: string): string {
	const [op, ...rest] = field.split('.');
	const tail = rest.join('.');
	if (!tail)
		return field === 'writer_commit_interval_seconds' ? 'writerCommitIntervalSeconds' : field;
	const key = FORM_KEY[tail];
	if (key) return `${op}.${key}`;
	// An operation enabled without any trigger: the message sits on its
	// first trigger field.
	if (tail === 'trigger') return `${op}.${firstTrigger(op as MaintenanceOperation)}`;
	return field;
}

function firstTrigger(op: MaintenanceOperation): keyof OperationForm {
	switch (op) {
		case 'optimize':
			return 'smallFilePercent';
		case 'optimize_manifests':
			return 'manifests';
		case 'expire_snapshots':
			return 'snapshots';
		default:
			return 'maxIntervalMinutes';
	}
}

const NUMERIC: (keyof OperationForm)[] = [
	'smallFilePercent',
	'deleteFiles',
	'manifests',
	'snapshots',
	'oldestAgeHours',
	'minIntervalMinutes',
	'maxIntervalMinutes',
	'startHour',
	'endHour',
	'timeoutMinutes',
	'targetFileSizeMB',
	'skipFilesNewerThanMinutes',
	'retentionHours',
	'retainLast',
	'olderThanHours'
];

/** Form keys holding something that is not a number, before any request. */
export function notNumbers(form: MaintenanceForm): string[] {
	const out: string[] = [];
	const bad = (v: string) => v.trim() !== '' && !Number.isFinite(Number(v.trim()));
	if (bad(form.writerCommitIntervalSeconds)) out.push('writerCommitIntervalSeconds');
	for (const [op, f] of Object.entries(form.operations)) {
		if (!f.enabled) continue;
		for (const key of NUMERIC) if (bad(f[key] as string)) out.push(`${op}.${key}`);
	}
	return out;
}
