import { describe, expect, it } from 'vitest';
import { makeMaintenancePolicy, makeMaintenanceSettings } from '../../test/factories';
import { emptyPolicy } from './client';
import { formKey, fromForm, fromTableProperties, notNumbers, toForm } from './form';

describe('maintenance form', () => {
	it('round-trips parameters through readable units', () => {
		const policy = makeMaintenancePolicy({
			optimize: makeMaintenanceSettings({
				enabled: true,
				trigger: { small_file_ratio: 0.3, delete_files: 200 },
				min_interval_seconds: 3600,
				params: {
					target_file_size_bytes: 256 * 1024 * 1024,
					skip_files_newer_than_seconds: 1800,
					partition_filter: "day < DATE '2026-05-01'",
					sorted_by: ['id', 'ts']
				}
			}),
			remove_orphan_files: makeMaintenanceSettings({
				enabled: true,
				max_interval_seconds: 604800,
				window: { start_hour: 22, end_hour: 6, tz: 'America/Sao_Paulo' },
				on_conflict: 'give_up_and_report',
				params: { older_than_seconds: 86400 }
			})
		});
		const form = toForm(policy);
		expect(form.operations.optimize.smallFilePercent).toBe('30');
		expect(form.operations.optimize.targetFileSizeMB).toBe('256');
		expect(form.operations.optimize.skipFilesNewerThanMinutes).toBe('30');
		expect(form.operations.remove_orphan_files.maxIntervalMinutes).toBe('10080');
		expect(form.operations.expire_snapshots.retentionHours).toBe('24');

		const back = fromForm(form);
		expect(back.optimize.trigger).toEqual({ small_file_ratio: 0.3, delete_files: 200 });
		expect(back.optimize.params).toEqual(policy.optimize.params);
		expect(back.remove_orphan_files.window).toEqual(policy.remove_orphan_files.window);
		expect(back.remove_orphan_files.on_conflict).toBe('give_up_and_report');
		expect(back.expire_snapshots.params.retention_seconds).toBe(86400);
	});

	it('sends each operation only its own triggers and params', () => {
		const back = fromForm(toForm(makeMaintenancePolicy()));
		expect(Object.keys(back.optimize_manifests.params)).toEqual([]);
		expect(Object.keys(back.remove_orphan_files.trigger)).toEqual([]);
		expect(back.expire_snapshots.params.target_file_size_bytes).toBeUndefined();
	});

	it("starts new parameters from the table's Iceberg properties", () => {
		const out = fromTableProperties(emptyPolicy(), {
			'history.expire.max-snapshot-age-ms': '432000000',
			'history.expire.min-snapshots-to-keep': '5',
			'write.target-file-size-bytes': '134217728'
		});
		expect(out.expire_snapshots.params.retention_seconds).toBe(432000);
		expect(out.expire_snapshots.params.retain_last).toBe(5);
		expect(out.optimize.params.target_file_size_bytes).toBe(134217728);
	});

	it('names each API field by its form key', () => {
		expect(formKey('expire_snapshots.params.retention_seconds')).toBe(
			'expire_snapshots.retentionHours'
		);
		expect(formKey('optimize.window.tz')).toBe('optimize.timeZone');
		expect(formKey('optimize_manifests.trigger')).toBe('optimize_manifests.manifests');
		expect(formKey('remove_orphan_files.trigger')).toBe('remove_orphan_files.maxIntervalMinutes');
		expect(formKey('writer_commit_interval_seconds')).toBe('writerCommitIntervalSeconds');
		expect(formKey('mode')).toBe('mode');
	});

	it('finds non-numeric input in enabled operations only', () => {
		const form = toForm(makeMaintenancePolicy());
		form.operations.expire_snapshots.snapshots = 'lots';
		form.operations.optimize.deleteFiles = 'x';
		expect(notNumbers(form)).toEqual(['expire_snapshots.snapshots']);
	});
});
