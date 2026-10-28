<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import ActivityPanel from '$lib/audit/ActivityPanel.svelte';
	import {
		deleteEngine,
		discoverEngineCatalogs,
		engineBindings,
		engineImpact,
		engineObservations,
		getEngine,
		listReconcileRuns,
		reconcileEngine,
		testEngine,
		updateEngine,
		type CatalogBinding,
		type DiscoverResult,
		type Engine,
		type EngineImpact,
		type EngineObservation,
		type ReconcileAction,
		type ReconcileRun,
		type TestResult
	} from '$lib/engines/admin';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Drawer from '$lib/ui/Drawer.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import EngineBadge from '$lib/ui/patterns/EngineBadge.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import { describeGuarantees } from '$lib/engines/guarantees';

	type Tab = 'overview' | 'catalogs' | 'observed' | 'reconcile' | 'activity';

	const engineId = $derived(page.params.engine_id ?? '');

	let tab = $state<Tab>('overview');
	let engine = $state<Engine | null>(null);
	let impact = $state<EngineImpact | null>(null);
	let bindings = $state<CatalogBinding[]>([]);
	let observations = $state<EngineObservation[]>([]);
	let runs = $state<ReconcileRun[]>([]);
	let loading = $state(true);
	let error = $state('');

	let testOpen = $state(false);
	let testing = $state(false);
	let testResult = $state<TestResult | null>(null);
	let testError = $state('');

	let confirmOpen = $state(false);
	let pending = $state<'enable' | 'disable' | 'delete'>('disable');

	let discovering = $state(false);
	let discoverResult = $state<DiscoverResult | null>(null);
	let discoverError = $state('');

	let driftLoading = $state(false);
	let driftError = $state('');
	let report = $state<ReconcileRun | null>(null);
	let applyOpen = $state(false);

	let runDrawer = $state(false);
	let selectedRun = $state<ReconcileRun | null>(null);

	const federated = $derived(engine?.class === 'federated');
	const guarantees = $derived(engine ? describeGuarantees(engine) : []);
	const mutating = $derived(
		(report?.actions ?? []).filter((a) => a.kind !== 'none' && a.result !== 'reported')
	);

	function healthStatus(e: Engine): string {
		if (!e.enabled) return 'disabled';
		return e.health?.status ?? 'unknown';
	}

	async function load() {
		if (!engineId) return;
		loading = true;
		error = '';
		try {
			engine = await getEngine(engineId);
			impact = await engineImpact(engineId);
			bindings = await engineBindings(engineId);
			if (engine.class === 'federated') {
				observations = await engineObservations(engineId);
				runs = await listReconcileRuns(engineId);
			}
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function refresh() {
		if (!engineId) return;
		engine = await getEngine(engineId);
		impact = await engineImpact(engineId);
		bindings = await engineBindings(engineId);
	}

	function selectTab(next: Tab) {
		tab = next;
		window.history.replaceState(null, '', `${page.url.pathname}?tab=${next}`);
	}

	async function runTest() {
		if (!engineId) return;
		testing = true;
		testError = '';
		testResult = null;
		try {
			testResult = await testEngine(engineId);
			testOpen = true;
			await refresh();
		} catch (e) {
			testError = e instanceof Error ? e.message : String(e);
			testOpen = true;
		} finally {
			testing = false;
		}
	}

	function ask(action: 'enable' | 'disable' | 'delete') {
		pending = action;
		confirmOpen = true;
	}

	async function confirmAction() {
		if (!engineId || !engine) return;
		if (pending === 'delete') {
			await deleteEngine(engineId);
			window.location.href = '/engines';
			return;
		}
		await updateEngine(engineId, { enabled: pending === 'enable' });
		await refresh();
	}

	async function discover() {
		if (!engineId) return;
		discovering = true;
		discoverError = '';
		discoverResult = null;
		try {
			discoverResult = await discoverEngineCatalogs(engineId);
			await refresh();
		} catch (e) {
			discoverError = e instanceof Error ? e.message : String(e);
		} finally {
			discovering = false;
		}
	}

	async function checkDrift() {
		if (!engineId) return;
		driftLoading = true;
		driftError = '';
		try {
			report = await reconcileEngine(engineId, true);
			runs = await listReconcileRuns(engineId);
		} catch (e) {
			driftError = e instanceof Error ? e.message : String(e);
		} finally {
			driftLoading = false;
		}
	}

	async function applyDrift() {
		if (!engineId) return;
		try {
			report = await reconcileEngine(engineId, false);
			runs = await listReconcileRuns(engineId);
			applyOpen = false;
		} catch (e) {
			driftError = e instanceof Error ? e.message : String(e);
			applyOpen = false;
		}
	}

	function counts(run: ReconcileRun): string {
		const out: Record<string, number> = {};
		for (const action of run.actions) out[action.kind] = (out[action.kind] ?? 0) + 1;
		return Object.entries(out)
			.map(([kind, n]) => `${kind} ${n}`)
			.join(' · ');
	}

	function actionTone(action: ReconcileAction): 'neutral' | 'success' | 'warning' | 'danger' {
		if (action.result === 'failed') return 'danger';
		if (action.kind === 'none') return 'neutral';
		return action.result === 'applied' ? 'success' : 'warning';
	}

	function consequences(): string[] {
		if (!engine || !impact) return [];
		if (pending === 'delete') {
			return [
				`${impact.native_catalogs} native catalog(s) and their bindings are deleted with the engine.`,
				`${impact.managed_bindings} managed binding(s) block deletion while they exist.`,
				`${impact.running_executions} running execution(s) block deletion.`,
				`${impact.grants} grant(s), ${impact.metrics} metric(s) and ${impact.saved_queries_targeting} saved quer(ies) target its catalogs.`
			];
		}
		return [
			pending === 'disable'
				? 'New queries stop using this engine until it is enabled again.'
				: 'The engine becomes available for queries again.',
			`${impact.native_catalogs} native catalog(s), ${impact.managed_bindings} managed binding(s).`,
			`${impact.running_executions} execution(s) are running now.`
		];
	}

	onMount(() => {
		const requested = page.url.searchParams.get('tab');
		if (
			requested &&
			['overview', 'catalogs', 'observed', 'reconcile', 'activity'].includes(requested)
		) {
			tab = requested as Tab;
		}
		void load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	{#if loading && !engine}
		<div class="flex flex-col gap-2" role="status" aria-label={m.engines_loading_the_engine()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-14 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title="Could not load the engine" message={error} onretry={load} />
	{:else if engine}
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div>
				<div class="flex items-center gap-2">
					<EngineBadge {engine} />
					<StatusBadge kind="engine" status={healthStatus(engine)} />
					{#if !engine.enabled}
						<Badge tone="neutral">Disabled</Badge>
					{/if}
				</div>
				<p class="mt-1 text-[12px] text-slate-600 dark:text-slate-300">
					{engine.connection?.name ?? engine.connection_id} · {engine.catalog_count ?? 0} catalog(s)
					{#if engine.health?.checked_at}
						· checked <RelativeTime at={engine.health.checked_at} />
					{/if}
				</p>
			</div>
			<div class="flex flex-wrap gap-2">
				<Button loading={testing} disabled={testing} onclick={runTest}>Test</Button>
				<Button onclick={() => ask(engine?.enabled ? 'disable' : 'enable')}>
					{engine.enabled ? 'Disable' : 'Enable'}
				</Button>
				<Button variant="danger" onclick={() => ask('delete')}>Delete</Button>
			</div>
		</div>

		<div class="mb-4 flex gap-1 border-b border-border" role="tablist" aria-label="Engine tabs">
			{#each [['overview', 'Overview'], ['catalogs', 'Catalogs'], ...(federated ? [['observed', m.engines_observed()], ['reconcile', m.settings_system_reconcile()]] : []), ['activity', 'Activity']] as [value, label] (value)}
				<button
					type="button"
					role="tab"
					aria-selected={tab === value}
					class="-mb-px border-b-2 px-3 py-1.5 text-[12px] font-semibold {tab === value
						? 'border-primary text-primary'
						: 'border-transparent text-slate-600 hover:text-text-main dark:text-slate-300'}"
					onclick={() => selectTab(value as Tab)}
				>
					{label}
				</button>
			{/each}
		</div>

		{#if tab === 'overview'}
			<div class="grid gap-4 lg:grid-cols-2">
				<div class="rounded-lg border border-border bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold">Details</h2>
					<dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
						<dt class="text-slate-600 dark:text-slate-300">Kind</dt>
						<dd class="font-mono">{engine.kind}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Class</dt>
						<dd>{engine.class}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Version</dt>
						<dd class="font-mono">{engine.version ?? '—'}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Connection</dt>
						<dd>{engine.connection?.name ?? engine.connection_id}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Identity mode</dt>
						<dd>{engine.identity_mode}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Created</dt>
						<dd><RelativeTime at={engine.created_at} /></dd>
						{#if engine.health?.error_code}
							<dt class="text-slate-600 dark:text-slate-300">Last error</dt>
							<dd class="font-mono text-danger">{engine.health.error_code}</dd>
						{/if}
					</dl>
				</div>
				<div class="rounded-lg border border-border bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold">Guarantees</h2>
					<ul class="mt-2 flex flex-col gap-2">
						{#each guarantees as guarantee (guarantee.title)}
							<li
								class="border-l-2 pl-2 {guarantee.tone === 'success'
									? 'border-success'
									: guarantee.tone === 'warning'
										? 'border-amber-500'
										: guarantee.tone === 'danger'
											? 'border-danger'
											: guarantee.tone === 'info'
												? 'border-primary'
												: 'border-border'}"
							>
								<span class="block text-[11px] font-semibold">{guarantee.title}</span>
								<span class="block text-[11px] text-slate-600 dark:text-slate-300"
									>{guarantee.body}</span
								>
							</li>
						{/each}
					</ul>
				</div>
			</div>
		{:else if tab === 'catalogs'}
			<div class="mb-3 flex flex-wrap items-center justify-between gap-2">
				<p class="text-[12px] text-slate-600 dark:text-slate-300">
					{engine.class === 'direct'
						? m.engines_native_catalogs_discovered_on_this_engine()
						: m.engines_catalogs_hosted_by_this_engine_managed_by()}
				</p>
				{#if engine.class === 'direct'}
					<Button loading={discovering} disabled={discovering} onclick={discover}
						>Discover catalogs</Button
					>
				{/if}
			</div>
			{#if discoverResult}
				<div class="mb-3 rounded-lg border border-border bg-bg-panel p-3 text-[11px]">
					<p class="font-semibold">Discovery finished</p>
					<p class="mt-1">Added: {discoverResult.added.join(', ') || 'none'}</p>
					<p>Disabled: {discoverResult.disabled.join(', ') || 'none'}</p>
					<p>Unchanged: {discoverResult.unchanged.join(', ') || 'none'}</p>
				</div>
			{/if}
			{#if discoverError}
				<p class="mb-3 text-[11px] text-danger" role="alert">{discoverError}</p>
			{/if}
			{#if bindings.length === 0}
				<EmptyState title="No catalogs" description="This engine has no binding yet." />
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">Catalog</th>
								<th class="px-3 py-2">Mode</th>
								<th class="px-3 py-2">Status</th>
								<th class="px-3 py-2">Enabled</th>
								<th class="px-3 py-2 text-right">Data</th>
							</tr>
						</thead>
						<tbody>
							{#each bindings as binding (binding.id)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-3 py-1.5 font-mono">{binding.engine_catalog_name}</td>
									<td class="px-3 py-1.5"><Badge tone="neutral">{binding.mode}</Badge></td>
									<td class="px-3 py-1.5">
										<StatusBadge
											kind="binding"
											status={binding.lifecycle_state ?? (binding.enabled ? 'active' : 'disabled')}
										/>
									</td>
									<td class="px-3 py-1.5">{binding.enabled ? 'Yes' : 'No'}</td>
									<td class="px-3 py-1.5 text-right">
										<button
											type="button"
											class="text-primary hover:underline"
											onclick={() =>
												(window.location.href = `/data?catalog=${encodeURIComponent(binding.catalog_id)}`)}
										>
											Open data
										</button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{:else if tab === 'observed'}
			{#if observations.length === 0}
				<EmptyState
					title="No observation yet"
					description="The observation pass fills this tab when the engine reports its catalogs."
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">Catalog</th>
								<th class="px-3 py-2">Connector</th>
								<th class="px-3 py-2">Mode</th>
								<th class="px-3 py-2">Present</th>
								<th class="px-3 py-2">First seen</th>
								<th class="px-3 py-2">Last observed</th>
							</tr>
						</thead>
						<tbody>
							{#each observations as observation (observation.catalog_name)}
								<tr
									class="border-b border-border/60 last:border-b-0 {!observation.present &&
									observation.mode === 'managed'
										? 'bg-amber-50/60 dark:bg-[#241a06]/40'
										: ''}"
								>
									<td class="px-3 py-1.5 font-mono">{observation.catalog_name}</td>
									<td class="px-3 py-1.5">{observation.connector ?? '—'}</td>
									<td class="px-3 py-1.5"
										><Badge tone={observation.mode === 'managed' ? 'info' : 'neutral'}
											>{observation.mode}</Badge
										></td
									>
									<td class="px-3 py-1.5">
										{#if observation.present}
											<Badge tone="success">Present</Badge>
										{:else}
											<Badge tone="warning">Missing</Badge>
										{/if}
									</td>
									<td class="px-3 py-1.5"><RelativeTime at={observation.first_seen_at} /></td>
									<td class="px-3 py-1.5"><RelativeTime at={observation.observed_at} /></td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{:else if tab === 'reconcile'}
			<div class="mb-3 flex flex-wrap gap-2">
				<Button loading={driftLoading} disabled={driftLoading} onclick={checkDrift}
					>Check drift</Button
				>
				<span
					title={mutating.length === 0
						? m.engines_run_a_drift_check_with_changes_first()
						: undefined}
				>
					<Button
						variant="primary"
						disabled={mutating.length === 0}
						onclick={() => (applyOpen = true)}
					>
						Apply {mutating.length > 0 ? `(${mutating.length})` : ''}
					</Button>
				</span>
			</div>
			{#if driftError}
				<p class="mb-3 text-[11px] text-danger" role="alert">{driftError}</p>
			{/if}
			{#if report}
				<div class="mb-4 rounded-lg border border-border bg-bg-panel p-3">
					<p class="text-[12px] font-semibold">
						{report.dry_run ? m.engines_dry_run() : m.engines_applied()} · {counts(report) ||
							m.engines_no_actions()}
						{#if report.max_actions != null}
							· cap {report.max_actions}
						{/if}
					</p>
					{#if report.error}
						<p class="mt-1 text-[11px] text-danger">{report.error}</p>
					{/if}
					<table class="mt-2 w-full text-left text-[11px]">
						<thead class="text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300">
							<tr>
								<th class="py-1">Catalog</th>
								<th class="py-1">Action</th>
								<th class="py-1">Reason</th>
								<th class="py-1">Result</th>
							</tr>
						</thead>
						<tbody>
							{#each report.actions as action, index (`${action.catalog}-${action.kind}-${index}`)}
								<tr class="border-t border-border/60">
									<td class="py-1 font-mono">{action.catalog}</td>
									<td class="py-1"><Badge tone={actionTone(action)}>{action.kind}</Badge></td>
									<td class="py-1">{action.reason}</td>
									<td class="py-1"
										>{action.result}{action.error_code ? ` (${action.error_code})` : ''}</td
									>
								</tr>
							{/each}
						</tbody>
					</table>
					{#if report.adopted.length > 0}
						<p class="mt-2 text-[11px] text-slate-600 dark:text-slate-300">
							Adopted (never changed by Baleia): <span class="font-mono"
								>{report.adopted.join(', ')}</span
							>
						</p>
					{/if}
				</div>
			{/if}

			<h2 class="mb-2 text-[13px] font-semibold">Run history</h2>
			{#if runs.length === 0}
				<EmptyState title="No reconcile run" description="Check drift to record the first run." />
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">Started</th>
								<th class="px-3 py-2">Mode</th>
								<th class="px-3 py-2">Actions</th>
								<th class="px-3 py-2">Error</th>
							</tr>
						</thead>
						<tbody>
							{#each runs as run (run.id)}
								<tr
									class="cursor-pointer border-b border-border/60 last:border-b-0 hover:bg-bg-toolbar"
									onclick={() => {
										selectedRun = run;
										runDrawer = true;
									}}
									onkeydown={(e) => {
										if (e.key === 'Enter') {
											selectedRun = run;
											runDrawer = true;
										}
									}}
									tabindex="0"
								>
									<td class="px-3 py-1.5"><RelativeTime at={run.started_at} /></td>
									<td class="px-3 py-1.5">{run.dry_run ? m.engines_dry_run() : m.common_apply()}</td
									>
									<td class="px-3 py-1.5">{counts(run) || '—'}</td>
									<td class="px-3 py-1.5">{run.error ?? '—'}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{:else if tab === 'activity'}
			<ActivityPanel resourceType="engine" resourceId={engineId} title="Engine activity" />
		{/if}
	{/if}
</div>

<Modal bind:open={testOpen} title={m.engines_engine_test()}>
	{#if testError}
		<p class="text-[12px] text-danger" role="alert">{testError}</p>
	{:else if testResult}
		<div class="flex flex-col gap-2 text-[12px]">
			<p class="font-semibold">
				{testResult.success ? m.connections_all_checks_passed() : m.connections_checks_failed()}
				<span class="ml-1 font-normal text-slate-600 dark:text-slate-300"
					>{testResult.latency_ms} ms</span
				>
			</p>
			{#if testResult.error_message}
				<p class="text-danger">{testResult.error_message}</p>
			{/if}
			<ul class="flex flex-col gap-1">
				{#each testResult.checks ?? [] as check (check.name)}
					<li class="flex items-start gap-2">
						<Badge tone={check.skipped ? 'neutral' : check.ok ? 'success' : 'danger'}>
							{check.skipped ? m.statusbadge_skipped() : check.ok ? 'OK' : 'Failed'}
						</Badge>
						<span class="font-mono">{check.name}</span>
						<span class="text-slate-600 dark:text-slate-300"
							>{check.detail ?? check.error_code ?? ''}</span
						>
					</li>
				{/each}
			</ul>
		</div>
	{/if}
</Modal>

<ConfirmDialog
	bind:open={confirmOpen}
	title={pending === 'delete'
		? m.engines_delete_engine()
		: pending === 'disable'
			? m.engines_disable_engine()
			: m.engines_enable_engine()}
	resourceName={engine?.name ?? ''}
	confirmLabel={pending === 'delete'
		? m.engines_delete_engine()
		: pending === 'disable'
			? 'Disable'
			: 'Enable'}
	typeToConfirm={pending === 'delete'}
	danger={pending !== 'enable'}
	consequences={consequences()}
	onconfirm={confirmAction}
/>

<ConfirmDialog
	bind:open={applyOpen}
	title={m.engines_apply_reconcile()}
	resourceName={engine?.name ?? ''}
	confirmLabel="Apply changes"
	consequences={[
		...mutating.map((a) => `${a.kind} ${a.catalog} (${a.reason})`),
		`At most ${report?.max_actions ?? m.engines_the_configured_cap()} changes run; the rest are reported.`,
		'Catalogs Baleia does not manage are adopted and never changed.'
	]}
	onconfirm={applyDrift}
/>

<Drawer size="lg" bind:open={runDrawer} title={m.engines_reconcile_run()}>
	{#if selectedRun}
		<p class="text-[11px] text-slate-600 dark:text-slate-300">
			{selectedRun.dry_run ? m.engines_dry_run() : m.common_apply()}
			{m.engines_started()}
			<RelativeTime at={selectedRun.started_at} /> · {counts(selectedRun)}
		</p>
		{#if selectedRun.error}
			<p class="mt-2 text-[11px] text-danger">{selectedRun.error}</p>
		{/if}
		<table class="mt-3 w-full text-left text-[11px]">
			<thead class="text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300">
				<tr>
					<th class="py-1">{m.common_catalog()}</th>
					<th class="py-1">{m.common_action()}</th>
					<th class="py-1">{m.common_reason()}</th>
					<th class="py-1">{m.engines_result()}</th>
				</tr>
			</thead>
			<tbody>
				{#each selectedRun.actions as action, index (`${action.catalog}-${index}`)}
					<tr class="border-t border-border/60">
						<td class="py-1 font-mono">{action.catalog}</td>
						<td class="py-1">{action.kind}</td>
						<td class="py-1">{action.reason}</td>
						<td class="py-1">{action.result}{action.error_code ? ` (${action.error_code})` : ''}</td
						>
					</tr>
				{/each}
			</tbody>
		</table>
		{#if selectedRun.adopted.length > 0}
			<p class="mt-2 text-[11px]">
				{m.engines_adopted()} <span class="font-mono">{selectedRun.adopted.join(', ')}</span>
			</p>
		{/if}
	{/if}
</Drawer>
