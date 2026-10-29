<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import { can } from '$lib/authz/can';
	import {
		MaintenanceRequestError,
		OPERATIONS,
		analyzeTableHealth,
		deleteTableMaintenance,
		dryRunTableMaintenance,
		emptyPolicy,
		getTableMaintenance,
		runTableMaintenance,
		saveTableMaintenance,
		type MaintenanceOperation,
		type MaintenancePlan,
		type MaintenancePlanStep,
		type TableMaintenance
	} from './client';
	import { fieldMessage } from './fieldMessage';
	import {
		PROPERTY_OF,
		formKey,
		fromForm,
		fromTableProperties,
		notNumbers,
		toForm,
		type MaintenanceForm,
		type TextKey
	} from './form';
	import { operationLabel, reasonText, triggerLabel } from './labels';

	let { catalogId, namespace, table }: { catalogId: string; namespace: string; table: string } =
		$props();

	const target = $derived({ catalogId, namespace, table });

	let loading = $state(true);
	let loadError = $state('');
	let data = $state<TableMaintenance | null>(null);
	let form = $state<MaintenanceForm | null>(null);
	let errors = $state<Record<string, string>>({});
	let saving = $state(false);
	let saveError = $state('');
	let removeOpen = $state(false);
	let planOpen = $state(false);
	let dryPlan = $state<MaintenancePlan | null>(null);
	let busy = $state('');
	let runOperation = $state<MaintenanceOperation | 'next_due'>('next_due');

	// A response for a table that is no longer the shown one is dropped.
	let loadedFor = '';

	async function load() {
		const key = `${catalogId}/${namespace}/${table}`;
		loadedFor = key;
		loading = true;
		loadError = '';
		try {
			const next = await getTableMaintenance(target);
			if (loadedFor !== key) return;
			data = next;
			form = toForm(next.policy ?? fromTableProperties(emptyPolicy(), next.properties));
			errors = {};
			saveError = '';
		} catch (e) {
			if (loadedFor === key) loadError = e instanceof Error ? e.message : String(e);
		} finally {
			if (loadedFor === key) loading = false;
		}
	}

	$effect(() => {
		void catalogId;
		void namespace;
		void table;
		void load();
	});

	async function save() {
		if (!form || !data) return;
		const bad = notNumbers(form);
		if (bad.length) {
			errors = Object.fromEntries(bad.map((key) => [key, m.field_invalid_format()]));
			saveError = m.maintenance_fix_fields();
			return;
		}
		saving = true;
		saveError = '';
		errors = {};
		try {
			await saveTableMaintenance(target, fromForm(form), data.policy?.revision);
			toasts.success(m.maintenance_saved());
			await load();
		} catch (e) {
			if (e instanceof MaintenanceRequestError && e.fields.length) {
				errors = Object.fromEntries(
					e.fields.map((f) => {
						const key = formKey(f.field);
						return [key, fieldMessage(f.code, f.params ?? undefined, key)];
					})
				);
				saveError = m.maintenance_fix_fields();
			} else {
				saveError = e instanceof Error ? e.message : String(e);
			}
		} finally {
			saving = false;
		}
	}

	async function remove() {
		await deleteTableMaintenance(target);
		toasts.success(m.maintenance_removed());
		await load();
	}

	async function act(name: string, run: () => Promise<void>) {
		busy = name;
		try {
			await run();
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			busy = '';
		}
	}

	const dryRun = () =>
		act('dry-run', async () => {
			dryPlan = await dryRunTableMaintenance(target);
			planOpen = true;
		});

	const runNow = () =>
		act('run', async () => {
			const run = await runTableMaintenance(target, runOperation);
			toasts.success(
				m.maintenance_run_started({ operation: operationLabel(run.operations[0] ?? '') })
			);
			await load();
		});

	const analyze = () =>
		act('analyze', async () => {
			await analyzeTableHealth(target);
			toasts.success(m.maintenance_analysis_started());
		});

	const modeOptions = $derived([
		{ value: 'managed', label: m.maintenance_mode_managed() },
		{ value: 'off', label: m.maintenance_mode_off() },
		{ value: 'maintained_elsewhere', label: m.maintenance_mode_elsewhere() }
	]);
	const conflictOptions = $derived([
		{ value: 'retry_with_backoff', label: m.maintenance_on_conflict_retry() },
		{ value: 'give_up_and_report', label: m.maintenance_on_conflict_give_up() }
	]);
	const runOptions = $derived([
		{ value: 'next_due', label: m.maintenance_run_next_due() },
		...OPERATIONS.filter((op) => form?.operations[op].enabled).map((op) => ({
			value: op,
			label: operationLabel(op)
		}))
	]);

	const formatVersion = $derived(data?.format_version ?? null);
	const canEdit = $derived(can(data, 'update'));
	const floorHours = $derived(
		data ? Math.round((data.retention_floor_seconds / 3600) * 100) / 100 : 0
	);

	function err(op: MaintenanceOperation | '', key: string): string | undefined {
		return errors[op ? `${op}.${key}` : key];
	}

	function propertyHint(op: MaintenanceOperation, key: TextKey): string | undefined {
		const property = PROPERTY_OF[`${op}.${key}`];
		const value = property ? data?.properties?.[property] : undefined;
		return property && value !== undefined
			? m.maintenance_from_property({ property, value })
			: undefined;
	}

	function stepTone(step: MaintenancePlanStep): 'success' | 'info' | 'neutral' {
		return step.would_run ? 'success' : step.due ? 'info' : 'neutral';
	}
</script>

{#snippet field(op: MaintenanceOperation, key: TextKey, label: string, placeholder = '')}
	{#if form}
		<div>
			<Input {label} bind:value={form.operations[op][key]} {placeholder} error={err(op, key)} />
			{#if propertyHint(op, key)}
				<p class="mt-0.5 text-[10px] text-text-muted">{propertyHint(op, key)}</p>
			{/if}
		</div>
	{/if}
{/snippet}

<section aria-labelledby="maintenance-heading" class="border-b border-border px-4 py-3">
	<div class="mb-2 flex items-center justify-between gap-2">
		<h3
			id="maintenance-heading"
			class="text-[10px] font-bold uppercase tracking-wide text-text-muted"
		>
			{m.maintenance_title()}
		</h3>
		{#if data?.policy}
			<Badge tone="neutral">{m.maintenance_revision({ revision: data.policy.revision })}</Badge>
		{/if}
	</div>

	{#if loading && !data}
		<div class="space-y-2" role="status" aria-label={m.maintenance_loading()}>
			<Skeleton class="h-4 w-1/2" />
			<Skeleton class="h-24 w-full" />
		</div>
	{:else if loadError}
		<ErrorState title={m.maintenance_could_not_load()} message={loadError} onretry={load} />
	{:else if form && data}
		{#if !data.policy}
			<p class="mb-3 text-[12px] text-text-muted">{m.maintenance_not_maintained()}</p>
		{/if}
		{#if !canEdit}
			<p class="mb-3 text-[12px] text-text-muted">{m.maintenance_read_only()}</p>
		{/if}

		<fieldset disabled={!canEdit} class="m-0 min-w-0 border-0 p-0">
			<div class="grid gap-3 sm:grid-cols-2">
				<Select
					label={m.maintenance_mode()}
					bind:value={form.mode}
					options={modeOptions}
					error={err('', 'mode')}
				/>
				<Input
					label={m.maintenance_writer_interval()}
					bind:value={form.writerCommitIntervalSeconds}
					error={err('', 'writerCommitIntervalSeconds')}
				/>
			</div>
			<p class="mt-1 text-[11px] text-text-muted">
				{#if form.mode === 'maintained_elsewhere'}
					{m.maintenance_mode_elsewhere_hint()}
				{:else if form.mode === 'off'}
					{m.maintenance_mode_off_hint()}
				{:else}
					{m.maintenance_mode_managed_hint()}
				{/if}
			</p>

			{#if form.mode !== 'maintained_elsewhere'}
				<div class="mt-3 flex flex-col gap-3">
					{#each OPERATIONS as op (op)}
						{@const f = form.operations[op]}
						<fieldset class="rounded-lg border border-border p-3">
							<legend class="px-1 text-[12px] font-semibold">{operationLabel(op)}</legend>
							<Checkbox bind:checked={f.enabled} label={m.maintenance_enabled()} />
							{#if f.enabled}
								<p class="mt-2 text-[10px] font-semibold uppercase tracking-wide text-text-muted">
									{m.maintenance_triggers()}
								</p>
								<div class="mt-1 grid gap-2 sm:grid-cols-3">
									{#if op === 'optimize'}
										{@render field(
											op,
											'smallFilePercent',
											m.maintenance_small_file_percent(),
											'30'
										)}
										{#if formatVersion === null || formatVersion >= 2}
											{@render field(op, 'deleteFiles', m.maintenance_delete_files(), '200')}
										{/if}
									{:else if op === 'optimize_manifests'}
										{@render field(op, 'manifests', m.maintenance_manifests(), '100')}
									{:else if op === 'expire_snapshots'}
										{@render field(op, 'snapshots', m.maintenance_snapshots(), '100')}
										{@render field(op, 'oldestAgeHours', m.maintenance_oldest_age_hours(), '72')}
									{:else}
										<p class="text-[11px] text-text-muted sm:col-span-3">
											{m.maintenance_orphans_interval_only()}
										</p>
									{/if}
								</div>

								<p class="mt-3 text-[10px] font-semibold uppercase tracking-wide text-text-muted">
									{m.maintenance_schedule()}
								</p>
								<div class="mt-1 grid gap-2 sm:grid-cols-3">
									{@render field(
										op,
										'minIntervalMinutes',
										m.maintenance_min_interval_minutes(),
										'60'
									)}
									{@render field(
										op,
										'maxIntervalMinutes',
										m.maintenance_max_interval_minutes(),
										'1440'
									)}
									{@render field(op, 'timeoutMinutes', m.maintenance_timeout_minutes(), '30')}
									<Select
										label={m.maintenance_on_conflict()}
										bind:value={f.onConflict}
										options={conflictOptions}
									/>
								</div>
								<div class="mt-2">
									<Checkbox bind:checked={f.windowEnabled} label={m.maintenance_window_enabled()} />
									{#if f.windowEnabled}
										<div class="mt-1 grid gap-2 sm:grid-cols-3">
											{@render field(op, 'startHour', m.maintenance_window_start(), '22')}
											{@render field(op, 'endHour', m.maintenance_window_end(), '6')}
											{@render field(op, 'timeZone', m.maintenance_window_tz(), 'UTC')}
										</div>
									{/if}
								</div>

								{#if op !== 'optimize_manifests'}
									<p class="mt-3 text-[10px] font-semibold uppercase tracking-wide text-text-muted">
										{m.maintenance_params()}
									</p>
									<div class="mt-1 grid gap-2 sm:grid-cols-2">
										{#if op === 'optimize'}
											{@render field(
												op,
												'targetFileSizeMB',
												m.maintenance_target_file_size_mb(),
												'256'
											)}
											{@render field(
												op,
												'skipFilesNewerThanMinutes',
												m.maintenance_skip_newer_minutes(),
												'60'
											)}
											{@render field(
												op,
												'partitionFilter',
												m.maintenance_partition_filter(),
												"day < DATE '2026-01-01'"
											)}
											{@render field(op, 'sortedBy', m.maintenance_sorted_by(), 'id, ts')}
										{:else if op === 'expire_snapshots'}
											{@render field(
												op,
												'retentionHours',
												m.maintenance_retention_hours(),
												String(floorHours)
											)}
											{@render field(op, 'retainLast', m.maintenance_retain_last(), '10')}
											<Checkbox
												bind:checked={f.cleanExpiredMetadata}
												label={m.maintenance_clean_expired_metadata()}
											/>
										{:else}
											{@render field(
												op,
												'olderThanHours',
												m.maintenance_older_than_hours(),
												String(floorHours)
											)}
										{/if}
									</div>
									{#if op !== 'optimize'}
										<p class="mt-1 text-[10px] text-text-muted">
											{m.maintenance_retention_floor({ hours: floorHours })}
										</p>
									{/if}
								{/if}
							{/if}
						</fieldset>
					{/each}
				</div>
			{/if}
		</fieldset>

		{#if saveError}
			<p role="alert" class="mt-3 text-[12px] text-danger">{saveError}</p>
		{/if}
		{#if canEdit}
			<div class="mt-3 flex flex-wrap items-center gap-2">
				<Button variant="primary" size="sm" loading={saving} onclick={save}
					>{m.maintenance_save()}</Button
				>
				<Button size="sm" onclick={load}>{m.maintenance_reset()}</Button>
				{#if data.policy}
					<Button size="sm" variant="danger" onclick={() => (removeOpen = true)}
						>{m.maintenance_remove()}</Button
					>
				{/if}
				<Button size="sm" loading={busy === 'analyze'} onclick={analyze}
					>{m.maintenance_analyze_now()}</Button
				>
			</div>
		{/if}

		{#if data.policy && data.policy.mode !== 'maintained_elsewhere'}
			<div class="mt-4">
				<p class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">
					{m.maintenance_plan_now()}
				</p>
				{#if data.plan}
					<ul class="mt-1 flex flex-col gap-1">
						{#each data.plan.plan as step (step.operation)}
							<li class="flex flex-wrap items-center gap-2 text-[12px]">
								<Badge tone={stepTone(step)}>{operationLabel(step.operation)}</Badge>
								<span class="text-text-muted">{reasonText(step)}</span>
							</li>
						{/each}
					</ul>
				{/if}
				{#if canEdit}
					<div class="mt-2 flex flex-wrap items-end gap-2">
						<Button size="sm" loading={busy === 'dry-run'} onclick={dryRun}
							>{m.maintenance_dry_run()}</Button
						>
						<div class="w-48">
							<Select
								label={m.maintenance_run_operation()}
								bind:value={runOperation}
								options={runOptions}
							/>
						</div>
						<Button size="sm" loading={busy === 'run'} onclick={runNow}
							>{m.maintenance_run_now()}</Button
						>
					</div>
				{/if}
			</div>
		{/if}

		<div class="mt-4">
			<p class="text-[10px] font-semibold uppercase tracking-wide text-text-muted">
				{m.maintenance_last_runs()}
			</p>
			{#if data.runs.length === 0}
				<p class="mt-1 text-[12px] text-text-muted">{m.maintenance_no_runs()}</p>
			{:else}
				<ul class="mt-1 flex flex-col gap-1">
					{#each data.runs as run (run.id)}
						<li class="flex flex-wrap items-center gap-2 text-[12px]">
							<StatusBadge kind="maintenance" status={run.status} />
							<span>{run.operations.map(operationLabel).join(', ') || '—'}</span>
							<Badge tone="neutral">{triggerLabel(run.trigger)}</Badge>
							<RelativeTime at={run.started_at} />
							{#if run.attempts > 1}
								<span class="text-text-muted"
									>{m.maintenance_attempts({ count: run.attempts })}</span
								>
							{/if}
							{#if run.error_code}
								<span class="font-mono text-danger">{run.error_code}</span>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}
</section>

<ConfirmDialog
	bind:open={removeOpen}
	title={m.maintenance_remove_title()}
	resourceName={`${namespace}.${table}`}
	consequences={[m.maintenance_remove_consequence()]}
	confirmLabel={m.maintenance_remove()}
	danger
	onconfirm={remove}
/>

<Modal bind:open={planOpen} title={m.maintenance_dry_run()}>
	{#if dryPlan}
		<ul class="flex flex-col gap-1.5">
			{#each dryPlan.plan as step (step.operation)}
				<li class="flex flex-wrap items-center gap-2 text-[12px]">
					<Badge tone={stepTone(step)}>{operationLabel(step.operation)}</Badge>
					<span class="text-text-muted">{reasonText(step)}</span>
				</li>
			{/each}
		</ul>
	{/if}
</Modal>
