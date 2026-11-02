<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onDestroy, onMount } from 'svelte';
	import { listEngines, type Engine } from '$lib/engines/admin';
	import { poll, type PollController } from '$lib/util/poll';
	import { toasts } from '$lib/ui/toast.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import Duration from '$lib/ui/patterns/Duration.svelte';
	import {
		createWorkloadRule,
		deleteWorkloadRule,
		getEngineQueues,
		listWorkloadRules,
		putEngineQueues,
		testWorkloadRule,
		updateWorkloadRule,
		type EngineQueues,
		type UpdateEngineQueuesRequest,
		type WorkloadRule,
		type WorkloadRuleMatch,
		type WorkloadRuleTestResult
	} from '$lib/workload/admin';

	type CapacityRow = {
		class: string;
		max_running: string;
		max_queued: string;
		queue_timeout_ms: string;
		runtime_timeout_ms: string;
		running: number;
		queued: number;
		oldest_wait_ms: number | null;
	};

	const classOptions = [
		{
			value: 'interactive',
			get label() {
				return m.workload_class_interactive();
			}
		},
		{
			value: 'heavy',
			get label() {
				return m.workload_class_heavy();
			}
		},
		{
			value: 'background',
			get label() {
				return m.workload_class_background();
			}
		}
	];
	const originOptions = [
		{
			value: 'editor',
			get label() {
				return m.workload_origin_editor();
			}
		},
		{
			value: 'agent',
			get label() {
				return m.workload_origin_agent();
			}
		},
		{
			value: 'explore',
			get label() {
				return m.workload_origin_explore();
			}
		},
		{
			value: 'retriever',
			get label() {
				return m.workload_origin_retriever();
			}
		},
		{
			value: 'metadata',
			get label() {
				return m.workload_origin_metadata();
			}
		},
		{
			value: 'maintenance',
			get label() {
				return m.workload_origin_maintenance();
			}
		}
	];
	const maxClassOptions = [
		{
			value: '',
			get label() {
				return m.workload_all();
			}
		},
		...classOptions
	];

	let engines = $state<Engine[]>([]);
	let engineId = $state('');
	let loading = $state(true);
	let error = $state('');

	let capacities = $state<CapacityRow[]>([]);
	let maxRunningTotal = $state('');
	let queuesLoaded = $state(false);
	let queuesError = $state('');
	let queuesPoll: PollController<EngineQueues> | undefined;

	let saving = $state(false);
	let saveError = $state('');
	let confirmClose = $state(false);
	let pendingSave = $state<UpdateEngineQueuesRequest | null>(null);

	let rules = $state<WorkloadRule[]>([]);
	let rulesError = $state('');
	let rulesBusy = $state(false);

	let ruleModal = $state(false);
	let editing = $state<WorkloadRule | null>(null);
	let formError = $state('');
	let form = $state({
		origin: '',
		workspace_id: '',
		team_id: '',
		role: '',
		user_id: '',
		agent_id: '',
		engine_id: '',
		saved_query_id: '',
		min_statements: '',
		max_statements: '',
		class: 'interactive',
		max_class: '',
		weight: '1',
		enabled: true
	});

	let deleting = $state<WorkloadRule | null>(null);
	let deleteOpen = $state(false);

	let testOrigin = $state('editor');
	let testing = $state(false);
	let testResult = $state<WorkloadRuleTestResult | null>(null);
	let testError = $state('');

	const sortedRules = $derived(
		[...rules].sort((a, b) => a.position - b.position || a.id.localeCompare(b.id))
	);

	function originLabel(value?: string | null): string {
		switch (value) {
			case 'editor':
				return m.workload_origin_editor();
			case 'agent':
				return m.workload_origin_agent();
			case 'explore':
				return m.workload_origin_explore();
			case 'retriever':
				return m.workload_origin_retriever();
			case 'metadata':
				return m.workload_origin_metadata();
			case 'maintenance':
				return m.workload_origin_maintenance();
			default:
				return m.workload_all();
		}
	}

	function classLabel(value?: string | null): string {
		switch (value) {
			case 'interactive':
				return m.workload_class_interactive();
			case 'heavy':
				return m.workload_class_heavy();
			case 'background':
				return m.workload_class_background();
			default:
				return m.workload_no_class();
		}
	}

	function shortId(value: string): string {
		return value.slice(0, 8);
	}

	function applyQueues(queues: EngineQueues, reset: boolean) {
		queuesError = '';
		if (reset) {
			maxRunningTotal = queues.max_running_total == null ? '' : String(queues.max_running_total);
			capacities = queues.classes.map((row) => ({
				class: row.class,
				max_running: String(row.max_running),
				max_queued: String(row.max_queued),
				queue_timeout_ms: String(row.queue_timeout_ms),
				runtime_timeout_ms: row.runtime_timeout_ms == null ? '' : String(row.runtime_timeout_ms),
				running: row.running,
				queued: row.queued,
				oldest_wait_ms: row.oldest_wait_ms ?? null
			}));
			queuesLoaded = true;
			return;
		}
		// A poll only refreshes the live columns; never overwrite what the
		// admin is editing (#547).
		capacities = capacities.map((row) => {
			const live = queues.classes.find((item) => item.class === row.class);
			return live
				? {
						...row,
						running: live.running,
						queued: live.queued,
						oldest_wait_ms: live.oldest_wait_ms ?? null
					}
				: row;
		});
	}

	function startQueues(id: string) {
		queuesPoll?.stop();
		queuesLoaded = false;
		queuesError = '';
		capacities = [];
		queuesPoll = poll(
			async () => {
				try {
					return await getEngineQueues(id);
				} catch (e) {
					// poll() swallows errors; record the failure so the view can
					// show it instead of a permanent skeleton (#589).
					if (id === engineId) queuesError = e instanceof Error ? e.message : String(e);
					throw e;
				}
			},
			{
				until: () => false,
				intervals: [5000],
				maxDurationMs: 24 * 60 * 60 * 1000,
				onValue: (queues) => {
					// Ignore a late response for an engine the admin switched
					// away from (#589).
					if (id !== engineId) return;
					applyQueues(queues, !queuesLoaded);
				}
			}
		);
	}

	$effect(() => {
		if (engineId) startQueues(engineId);
	});

	async function load() {
		loading = true;
		error = '';
		rulesError = '';
		try {
			engines = await listEngines();
			rules = await listWorkloadRules();
			if (!engineId && engines.length > 0) engineId = engines[0].id;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	onMount(load);
	onDestroy(() => queuesPoll?.stop());

	function numberField(value: unknown): number | null {
		const raw = String(value ?? '').trim();
		if (raw === '') return null;
		const n = Number(raw);
		return Number.isFinite(n) ? n : NaN;
	}

	function buildSave(): { body: UpdateEngineQueuesRequest; closesWithQueued: boolean } | null {
		saveError = '';
		const total = numberField(maxRunningTotal);
		if (total !== null && Number.isNaN(total)) {
			saveError = m.workload_could_not_save();
			return null;
		}
		const classes: UpdateEngineQueuesRequest['classes'] = [];
		let closesWithQueued = false;
		for (const row of capacities) {
			const maxRunning = numberField(row.max_running);
			const maxQueued = numberField(row.max_queued);
			const queueTimeout = numberField(row.queue_timeout_ms);
			const runtime = numberField(row.runtime_timeout_ms);
			if (
				maxRunning === null ||
				Number.isNaN(maxRunning) ||
				maxRunning < 0 ||
				maxQueued === null ||
				Number.isNaN(maxQueued) ||
				maxQueued < 0 ||
				queueTimeout === null ||
				Number.isNaN(queueTimeout) ||
				queueTimeout < 1 ||
				(runtime !== null && (Number.isNaN(runtime) || runtime < 1))
			) {
				saveError = m.workload_could_not_save();
				return null;
			}
			if (row.queued > 0 && maxRunning === 0) closesWithQueued = true;
			classes.push({
				class: row.class as 'interactive' | 'heavy' | 'background',
				max_running: maxRunning,
				max_queued: maxQueued,
				queue_timeout_ms: queueTimeout,
				runtime_timeout_ms: runtime
			});
		}
		return { body: { max_running_total: total, classes }, closesWithQueued };
	}

	async function save() {
		const plan = buildSave();
		if (!plan) return;
		pendingSave = plan.body;
		if (plan.closesWithQueued) {
			confirmClose = true;
			return;
		}
		await doSave();
	}

	async function doSave() {
		const body = pendingSave ?? buildSave()?.body ?? null;
		if (!body) {
			confirmClose = false;
			return;
		}
		saving = true;
		saveError = '';
		try {
			const queues = await putEngineQueues(engineId, body);
			applyQueues(queues, true);
			toasts.success(m.workload_saved());
		} catch (e) {
			saveError = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
			confirmClose = false;
			pendingSave = null;
		}
	}

	async function reloadRules() {
		try {
			rules = await listWorkloadRules();
			rulesError = '';
		} catch (e) {
			rulesError = e instanceof Error ? e.message : String(e);
		}
	}

	function resetForm() {
		form = {
			origin: '',
			workspace_id: '',
			team_id: '',
			role: '',
			user_id: '',
			agent_id: '',
			engine_id: '',
			saved_query_id: '',
			min_statements: '',
			max_statements: '',
			class: 'interactive',
			max_class: '',
			weight: '1',
			enabled: true
		};
		formError = '';
	}

	function openAdd() {
		editing = null;
		resetForm();
		ruleModal = true;
	}

	function openEdit(rule: WorkloadRule) {
		editing = rule;
		const match = rule.match;
		form = {
			origin: match.origin ?? '',
			workspace_id: match.workspace_id ?? '',
			team_id: match.team_id ?? '',
			role: match.role ?? '',
			user_id: match.user_id ?? '',
			agent_id: match.agent_id ?? '',
			engine_id: match.engine_id ?? '',
			saved_query_id: match.saved_query_id ?? '',
			min_statements: match.min_statements == null ? '' : String(match.min_statements),
			max_statements: match.max_statements == null ? '' : String(match.max_statements),
			class: rule.class,
			max_class: rule.max_class ?? '',
			weight: String(rule.weight),
			enabled: rule.enabled
		};
		formError = '';
		ruleModal = true;
	}

	function buildMatch(): WorkloadRuleMatch {
		const match: WorkloadRuleMatch = {};
		if (form.origin) match.origin = form.origin as NonNullable<WorkloadRuleMatch['origin']>;
		if (form.workspace_id.trim()) match.workspace_id = form.workspace_id.trim();
		if (form.team_id.trim()) match.team_id = form.team_id.trim();
		if (form.role.trim()) match.role = form.role.trim();
		if (form.user_id.trim()) match.user_id = form.user_id.trim();
		if (form.agent_id.trim()) match.agent_id = form.agent_id.trim();
		if (form.engine_id.trim()) match.engine_id = form.engine_id.trim();
		if (form.saved_query_id.trim()) match.saved_query_id = form.saved_query_id.trim();
		if (form.min_statements.trim()) match.min_statements = Number(form.min_statements);
		if (form.max_statements.trim()) match.max_statements = Number(form.max_statements);
		return match;
	}

	async function saveRule() {
		formError = '';
		const weight = Number(form.weight);
		if (!Number.isInteger(weight) || weight < 1) {
			formError = m.workload_could_not_save();
			return;
		}
		rulesBusy = true;
		try {
			const body = {
				match: buildMatch(),
				class: form.class as 'interactive' | 'heavy' | 'background',
				max_class: form.max_class
					? (form.max_class as 'interactive' | 'heavy' | 'background')
					: null,
				weight,
				enabled: form.enabled
			};
			if (editing) await updateWorkloadRule(editing.id, body);
			else await createWorkloadRule(body);
			ruleModal = false;
			await reloadRules();
			toasts.success(m.workload_rule_saved());
		} catch (e) {
			formError = e instanceof Error ? e.message : String(e);
		} finally {
			rulesBusy = false;
		}
	}

	async function move(rule: WorkloadRule, delta: number) {
		const ordered = [...sortedRules];
		const index = ordered.findIndex((item) => item.id === rule.id);
		const swap = index + delta;
		if (index < 0 || swap < 0 || swap >= ordered.length) return;
		[ordered[index], ordered[swap]] = [ordered[swap], ordered[index]];
		rulesBusy = true;
		try {
			await Promise.all(
				ordered.map((item, position) =>
					item.position === position + 1
						? Promise.resolve()
						: updateWorkloadRule(item.id, { position: position + 1 })
				)
			);
			await reloadRules();
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
			// A failed PATCH can leave duplicate positions; show the stored
			// order instead of a local one the server does not have (#589).
			await reloadRules();
		} finally {
			rulesBusy = false;
		}
	}

	async function toggle(rule: WorkloadRule) {
		rulesBusy = true;
		try {
			await updateWorkloadRule(rule.id, { enabled: !rule.enabled });
			await reloadRules();
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			rulesBusy = false;
		}
	}

	function askDelete(rule: WorkloadRule) {
		deleting = rule;
		deleteOpen = true;
	}

	async function confirmDelete() {
		if (!deleting) return;
		await deleteWorkloadRule(deleting.id);
		deleteOpen = false;
		deleting = null;
		await reloadRules();
		toasts.success(m.workload_rule_deleted());
	}

	async function runTest() {
		testing = true;
		testError = '';
		testResult = null;
		try {
			testResult = await testWorkloadRule({
				origin: testOrigin as NonNullable<WorkloadRuleMatch['origin']>
			});
		} catch (e) {
			testError = e instanceof Error ? e.message : String(e);
		} finally {
			testing = false;
		}
	}

	function matchSummary(rule: WorkloadRule): string {
		const match = rule.match;
		const parts: string[] = [];
		if (match.origin) parts.push(`${m.workload_rule_match_origin()}: ${originLabel(match.origin)}`);
		if (match.workspace_id)
			parts.push(`${m.workload_rule_match_workspace()}: ${shortId(match.workspace_id)}`);
		if (match.team_id) parts.push(`${m.workload_rule_match_team()}: ${shortId(match.team_id)}`);
		if (match.role) parts.push(`${m.workload_rule_match_role()}: ${match.role}`);
		if (match.user_id) parts.push(`${m.workload_rule_match_user()}: ${shortId(match.user_id)}`);
		if (match.agent_id) parts.push(`${m.workload_rule_match_agent()}: ${shortId(match.agent_id)}`);
		if (match.engine_id)
			parts.push(`${m.workload_rule_match_engine()}: ${shortId(match.engine_id)}`);
		if (match.saved_query_id)
			parts.push(`${m.workload_rule_match_saved_query()}: ${shortId(match.saved_query_id)}`);
		if (match.min_statements != null)
			parts.push(`${m.workload_rule_match_min_statements()}: ${match.min_statements}`);
		if (match.max_statements != null)
			parts.push(`${m.workload_rule_match_max_statements()}: ${match.max_statements}`);
		return parts.length > 0 ? parts.join(' · ') : m.workload_all();
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="w-full">
		<div class="mb-5">
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.workload_title()}</h1>
			<p class="mt-1 text-[13px] text-text-muted">{m.workload_description()}</p>
		</div>

		{#if loading}
			<div class="flex flex-col gap-3" role="status" aria-label={m.workload_title()}>
				<Skeleton height="2rem" />
				<Skeleton height="2rem" />
				<Skeleton height="2rem" width="70%" />
			</div>
		{:else if error}
			<ErrorState title={m.workload_could_not_load()} message={error} onretry={load} />
		{:else if engines.length === 0}
			<EmptyState title={m.workload_no_engines()} description={m.workload_choose_engine()} />
		{:else}
			<div class="mb-5 flex flex-wrap items-end gap-3">
				<label class="flex min-w-[220px] flex-col gap-1 text-[11px] font-medium text-text-muted">
					<span>{m.workload_engine()}</span>
					<select
						aria-label={m.workload_engine()}
						value={engineId}
						onchange={(e) => (engineId = (e.currentTarget as HTMLSelectElement).value)}
						class="rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
					>
						{#each engines as engine (engine.id)}
							<option value={engine.id}>{engine.name}</option>
						{/each}
					</select>
				</label>
			</div>

			<section class="mb-7 rounded-lg border border-border bg-bg-panel">
				<div class="flex flex-wrap items-end justify-between gap-3 border-b border-border p-4">
					<div>
						<h2 class="text-sm font-semibold">{m.workload_capacities()}</h2>
						<p class="mt-1 text-[12px] text-text-muted">{m.workload_live_queues_description()}</p>
					</div>
					<label class="flex w-[140px] flex-col gap-1 text-[11px] font-medium text-text-muted">
						<span>{m.workload_max_running_total()}</span>
						<input
							type="number"
							min="0"
							aria-label={m.workload_max_running_total()}
							bind:value={maxRunningTotal}
							class="rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
						/>
					</label>
				</div>
				{#if queuesError}
					<ErrorState title={m.workload_could_not_load()} message={queuesError} />
				{:else if capacities.length === 0}
					<div class="p-4" role="status" aria-label={m.workload_capacities()}>
						<Skeleton height="2rem" />
					</div>
				{:else}
					<div class="overflow-x-auto">
						<table class="w-full text-left text-xs">
							<thead>
								<tr
									class="border-b border-border bg-bg-toolbar text-[10px] uppercase tracking-wide text-text-muted"
								>
									<th class="px-3 py-2 font-semibold">{m.workload_class()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_max_running()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_max_queued()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_queue_timeout_ms()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_running()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_queued()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_oldest_wait()}</th>
								</tr>
							</thead>
							<tbody>
								{#each capacities as row (row.class)}
									<tr class="border-b border-border last:border-b-0">
										<td class="px-3 py-2">
											<Badge tone="neutral">{classLabel(row.class)}</Badge>
										</td>
										<td class="px-3 py-2">
											<input
												type="number"
												min="0"
												aria-label="{m.workload_max_running()} {classLabel(row.class)}"
												bind:value={row.max_running}
												class="w-24 rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
											/>
										</td>
										<td class="px-3 py-2">
											<input
												type="number"
												min="0"
												aria-label="{m.workload_max_queued()} {classLabel(row.class)}"
												bind:value={row.max_queued}
												class="w-24 rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
											/>
										</td>
										<td class="px-3 py-2">
											<input
												type="number"
												min="1"
												aria-label="{m.workload_queue_timeout_ms()} {classLabel(row.class)}"
												bind:value={row.queue_timeout_ms}
												class="w-28 rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
											/>
										</td>
										<td class="px-3 py-2 text-text-muted">{row.running}</td>
										<td class="px-3 py-2 text-text-muted">{row.queued}</td>
										<td class="whitespace-nowrap px-3 py-2 text-text-muted">
											{#if row.oldest_wait_ms != null}
												<Duration ms={row.oldest_wait_ms} />
											{:else}
												—
											{/if}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
				<div class="flex items-center gap-3 border-t border-border p-4">
					<Button
						variant="primary"
						loading={saving}
						disabled={capacities.length === 0}
						onclick={save}
					>
						{m.workload_save()}
					</Button>
					{#if saveError}
						<span class="text-xs text-red-600">{saveError}</span>
					{/if}
				</div>
			</section>

			<section class="rounded-lg border border-border bg-bg-panel">
				<div class="flex flex-wrap items-end justify-between gap-3 border-b border-border p-4">
					<div>
						<h2 class="text-sm font-semibold">{m.workload_rules()}</h2>
						<p class="mt-1 text-[12px] text-text-muted">{m.workload_rules_description()}</p>
					</div>
					<Button variant="primary" onclick={openAdd}>{m.workload_add_rule()}</Button>
				</div>

				{#if rulesError}
					<ErrorState title={m.workload_could_not_load()} message={rulesError} />
				{:else if sortedRules.length === 0}
					<EmptyState
						title={m.workload_no_rules()}
						description={m.workload_no_rules_description()}
					/>
				{:else}
					<div class="overflow-x-auto">
						<table class="w-full text-left text-xs">
							<thead>
								<tr
									class="border-b border-border bg-bg-toolbar text-[10px] uppercase tracking-wide text-text-muted"
								>
									<th class="px-3 py-2 font-semibold">{m.workload_rule_position()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_rule_enabled()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_rule_match()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_rule_class()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_rule_max_class()}</th>
									<th class="px-3 py-2 font-semibold">{m.workload_rule_weight()}</th>
									<th class="px-3 py-2 font-semibold"
										><span class="sr-only">{m.workload_rules()}</span></th
									>
								</tr>
							</thead>
							<tbody>
								{#each sortedRules as rule, index (rule.id)}
									<tr class="border-b border-border last:border-b-0">
										<td class="px-3 py-2 font-mono text-text-muted">{rule.position}</td>
										<td class="px-3 py-2">
											<Checkbox
												checked={rule.enabled}
												label={m.workload_rule_enabled()}
												disabled={rulesBusy}
												onchange={() => toggle(rule)}
											/>
										</td>
										<td class="px-3 py-2 text-text-muted">{matchSummary(rule)}</td>
										<td class="px-3 py-2">{classLabel(rule.class)}</td>
										<td class="px-3 py-2 text-text-muted">
											{rule.max_class ? classLabel(rule.max_class) : m.workload_all()}
										</td>
										<td class="px-3 py-2 text-text-muted">{rule.weight}</td>
										<td class="px-3 py-2">
											<div class="flex items-center justify-end gap-1">
												<Button
													size="sm"
													disabled={rulesBusy || index === 0}
													onclick={() => move(rule, -1)}
												>
													{m.workload_rule_move_up()}
												</Button>
												<Button
													size="sm"
													disabled={rulesBusy || index === sortedRules.length - 1}
													onclick={() => move(rule, 1)}
												>
													{m.workload_rule_move_down()}
												</Button>
												<Button size="sm" onclick={() => openEdit(rule)}>
													{m.workload_edit_rule()}
												</Button>
												<Button size="sm" onclick={() => askDelete(rule)}>
													{m.workload_delete_rule()}
												</Button>
											</div>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}

				<div class="border-t border-border p-4">
					<h3 class="mb-2 text-sm font-semibold">{m.workload_rule_test()}</h3>
					<div class="flex flex-wrap items-end gap-3">
						<Select
							label={m.workload_rule_match_origin()}
							bind:value={testOrigin}
							options={originOptions}
							class="w-[180px]"
						/>
						<Button variant="primary" loading={testing} onclick={runTest}>
							{m.workload_rule_test()}
						</Button>
					</div>
					{#if testError}
						<p class="mt-2 text-xs text-red-600">{testError}</p>
					{:else if testResult}
						<p class="mt-2 text-xs text-text-muted">
							{m.workload_rule_test_result({
								class: classLabel(testResult.class),
								reason: testResult.reason
							})}
						</p>
					{/if}
				</div>
			</section>
		{/if}
	</div>
</div>

<Modal bind:open={ruleModal} title={editing ? m.workload_edit_rule() : m.workload_add_rule()}>
	<div class="flex flex-col gap-3">
		<label class="flex flex-col gap-1 text-[11px] font-medium text-text-muted">
			<span>{m.workload_rule_match_origin()}</span>
			<select
				aria-label={m.workload_rule_match_origin()}
				bind:value={form.origin}
				class="rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
			>
				<option value="">{m.workload_all()}</option>
				{#each originOptions as option (option.value)}
					<option value={option.value}>{option.label}</option>
				{/each}
			</select>
		</label>
		<div class="grid grid-cols-2 gap-3">
			<Input label={m.workload_rule_match_workspace()} bind:value={form.workspace_id} />
			<Input label={m.workload_rule_match_team()} bind:value={form.team_id} />
			<Input label={m.workload_rule_match_user()} bind:value={form.user_id} />
			<Input label={m.workload_rule_match_agent()} bind:value={form.agent_id} />
			<Input label={m.workload_rule_match_engine()} bind:value={form.engine_id} />
			<Input label={m.workload_rule_match_saved_query()} bind:value={form.saved_query_id} />
			<Input label={m.workload_rule_match_role()} bind:value={form.role} />
			<Input
				label={m.workload_rule_match_min_statements()}
				type="number"
				bind:value={form.min_statements}
			/>
			<Input
				label={m.workload_rule_match_max_statements()}
				type="number"
				bind:value={form.max_statements}
			/>
			<Input label={m.workload_rule_weight()} type="number" bind:value={form.weight} />
		</div>
		<div class="grid grid-cols-2 gap-3">
			<Select label={m.workload_rule_class()} bind:value={form.class} options={classOptions} />
			<Select
				label={m.workload_rule_max_class()}
				bind:value={form.max_class}
				options={maxClassOptions}
			/>
		</div>
		<Checkbox label={m.workload_rule_enabled()} bind:checked={form.enabled} />
		{#if formError}
			<p class="text-xs text-red-600">{formError}</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (ruleModal = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={rulesBusy} onclick={saveRule}>
				{m.common_save()}
			</Button>
		</div>
	{/snippet}
</Modal>

<ConfirmDialog
	bind:open={deleteOpen}
	title={m.workload_delete_rule()}
	resourceName={deleting ? matchSummary(deleting) : ''}
	consequences={[m.workload_delete_rule_confirm()]}
	confirmLabel={m.workload_delete_rule()}
	onconfirm={confirmDelete}
/>

<ConfirmDialog
	bind:open={confirmClose}
	title={m.workload_capacities()}
	resourceName={m.workload_engine()}
	consequences={[m.workload_close_class_confirm()]}
	confirmLabel={m.workload_save()}
	onconfirm={doSave}
/>
