<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import QueriesChart from '$lib/monitoring/QueriesChart.svelte';
	import { onDestroy, onMount } from 'svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { loadCatalogs, type MetadataCatalog } from '$lib/metadata/client';
	import { poll, type PollController } from '$lib/util/poll';
	import { formatDuration } from '$lib/util/time';
	import { formatBytes } from '$lib/util/size';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import Drawer from '$lib/ui/Drawer.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import {
		createMaintenance,
		getCluster,
		getQuery,
		killQuery,
		listHealth,
		listMaintenanceRuns,
		listMonitoringEngines,
		listQueries,
		type ClusterSeries,
		type MaintenanceRun,
		type MonitoringEngine,
		type MonitoringQuery,
		type MonitoringQueryDetail,
		type MonitoringQueryPage,
		type TableHealth
	} from '$lib/monitoring/client';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const isAdmin = $derived(authStore.user?.role === 'admin');

	let engines = $state<MonitoringEngine[]>([]);
	let engineId = $state('');
	let tab = $state<'cluster' | 'queries' | 'tables'>('cluster');
	let loading = $state(true);
	let error = $state('');

	let clusterWindow = $state('1h');
	let cluster = $state<ClusterSeries | null>(null);
	let clusterLoading = $state(false);
	let clusterError = $state('');

	let queryPage = $state<MonitoringQueryPage>({ items: [] });
	let queriesLoading = $state(false);
	let queriesError = $state('');
	let scope = $state('mine');
	let queryState = $state('');
	let queryText = $state('');
	let selectedQuery = $state<MonitoringQueryDetail | null>(null);
	let queryDrawer = $state(false);

	let catalogs = $state<MetadataCatalog[]>([]);
	let catalogId = $state('');
	let health = $state<TableHealth[]>([]);
	let healthLoading = $state(false);
	let healthError = $state('');
	let needs = $state('');
	let maxScore = $state('');
	let runs = $state<MaintenanceRun[]>([]);
	let maintainTarget = $state<TableHealth | null>(null);
	let maintainOpen = $state(false);
	let operations = $state<Record<string, boolean>>({
		optimize: true,
		expire_snapshots: false,
		remove_orphan_files: false
	});
	let retention = $state('7d');
	let maintaining = $state(false);
	let maintainError = $state('');

	let polls: PollController<unknown>[] = [];

	function stopPolls() {
		for (const controller of polls) controller.stop();
		polls = [];
	}

	async function loadEngines() {
		loading = true;
		error = '';
		try {
			engines = await listMonitoringEngines();
			engineId = engines[0]?.id ?? '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function loadCluster() {
		if (!engineId) return;
		clusterLoading = true;
		clusterError = '';
		try {
			cluster = await getCluster(engineId, clusterWindow);
		} catch (e) {
			clusterError = e instanceof Error ? e.message : String(e);
		} finally {
			clusterLoading = false;
		}
	}

	async function loadQueries() {
		if (!engineId) return;
		queriesLoading = true;
		queriesError = '';
		try {
			queryPage = await listQueries(engineId, {
				scope,
				state: queryState || undefined,
				q: queryText.trim() || undefined,
				limit: 50
			});
		} catch (e) {
			queriesError = e instanceof Error ? e.message : String(e);
		} finally {
			queriesLoading = false;
		}
	}

	async function loadCatalogList() {
		if (!workspaceId || catalogs.length > 0) return;
		try {
			catalogs = await loadCatalogs(workspaceId);
			catalogId = catalogs[0]?.id ?? '';
		} catch {
			catalogs = [];
		}
	}

	// The catalog the health rows and runs on screen belong to. Switching
	// catalog clears them, so no row of the old catalog stays actionable,
	// and a late response for a catalog no longer selected is dropped.
	let healthCatalog = '';

	async function loadHealth() {
		const target = catalogId;
		if (!target) return;
		if (healthCatalog !== target) {
			healthCatalog = target;
			health = [];
			runs = [];
		}
		healthLoading = true;
		healthError = '';
		try {
			const rows = await listHealth(target, {
				needs: needs || undefined,
				maxScore: maxScore ? Number(maxScore) : undefined
			});
			const list = await listMaintenanceRuns(target);
			if (catalogId !== target) return;
			health = rows;
			runs = list;
		} catch (e) {
			if (catalogId === target) healthError = e instanceof Error ? e.message : String(e);
		} finally {
			if (catalogId === target) healthLoading = false;
		}
	}

	async function loadActive() {
		stopPolls();
		if (!engineId && tab !== 'tables') return;
		if (tab === 'cluster') {
			await loadCluster();
			polls.push(
				poll(loadCluster, { until: () => false, intervals: [30_000], maxDurationMs: 3_600_000 })
			);
		} else if (tab === 'queries') {
			await loadQueries();
			polls.push(
				poll(loadQueries, { until: () => false, intervals: [10_000], maxDurationMs: 3_600_000 })
			);
		} else {
			await loadCatalogList();
			await loadHealth();
		}
	}

	function selectTab(next: 'cluster' | 'queries' | 'tables') {
		tab = next;
		void loadActive();
	}

	// The drawer opens at once on the clicked query (#480): a skeleton while
	// its detail loads, an error with a retry if it fails, and a response
	// for a query that is no longer the selected one is dropped.
	let queryOpening = $state<MonitoringQuery | null>(null);
	let queryLoading = $state(false);
	let queryError = $state('');

	async function openQuery(query: MonitoringQuery) {
		if (!engineId) return;
		queryOpening = query;
		selectedQuery = null;
		queryError = '';
		queryLoading = true;
		queryDrawer = true;
		try {
			const detail = await getQuery(engineId, query.trino_query_id);
			if (queryOpening === query) selectedQuery = detail;
		} catch (e) {
			if (queryOpening === query) queryError = e instanceof Error ? e.message : String(e);
		} finally {
			if (queryOpening === query) queryLoading = false;
		}
	}

	// Killing a query asks first and names it (FD4).
	let killing = $state<MonitoringQuery | null>(null);
	let killOpen = $state(false);

	function askKill(query: MonitoringQuery) {
		killing = query;
		killOpen = true;
	}

	async function kill() {
		const target = killing;
		if (!engineId || !target) return;
		// ConfirmDialog shows a thrown error and stays open.
		await killQuery(engineId, target.trino_query_id);
		toasts.success(m.monitoring_query_killed());
		await loadQueries();
	}

	function openMaintenance(row: TableHealth) {
		maintainTarget = row;
		operations = {
			optimize: row.needs_compaction,
			expire_snapshots: row.needs_snapshot_expiry,
			remove_orphan_files: row.needs_orphan_cleanup
		};
		if (!operations.optimize && !operations.expire_snapshots && !operations.remove_orphan_files) {
			operations.optimize = true;
		}
		retention = '7d';
		maintainError = '';
		maintainOpen = true;
	}

	async function submitMaintenance() {
		const target = maintainTarget;
		if (!target) return;
		// The row names its own catalog: never pair it with another selection.
		const targetCatalog = target.catalog_id;
		const selected = Object.entries(operations)
			.filter(([, enabled]) => enabled)
			.map(([operation]) => operation);
		if (selected.length === 0) {
			maintainError = m.monitoring_choose_at_least_one_operation();
			return;
		}
		maintaining = true;
		maintainError = '';
		try {
			const run = await createMaintenance(targetCatalog, {
				namespace: target.namespace,
				table: target.table_name,
				operations: selected,
				retention:
					operations.expire_snapshots || operations.remove_orphan_files ? retention : undefined
			});
			// A newer dialog may be open by now: only close the one that asked.
			if (maintainTarget === target) maintainOpen = false;
			if (catalogId === targetCatalog) runs = [run, ...runs];
			const controller = poll(
				async () => {
					const current = await listMaintenanceRuns(targetCatalog);
					if (catalogId === targetCatalog) runs = current;
					return current.find((item) => item.id === run.id);
				},
				{ until: (item) => !!item && item.status !== 'running', intervals: [1000, 2000, 5000] }
			);
			await controller.done;
			await loadHealth();
		} catch (e) {
			if (maintainTarget === target) maintainError = e instanceof Error ? e.message : String(e);
		} finally {
			maintaining = false;
		}
	}

	function needsLabels(row: TableHealth): string[] {
		const out: string[] = [];
		if (row.needs_compaction) out.push('compaction');
		if (row.needs_snapshot_expiry) out.push(m.monitoring_snapshot_expiry());
		if (row.needs_orphan_cleanup) out.push(m.monitoring_orphan_cleanup());
		return out;
	}

	function scoreTone(score: number): 'success' | 'warning' | 'danger' {
		return score >= 80 ? 'success' : score >= 50 ? 'warning' : 'danger';
	}

	onMount(async () => {
		await workspaceStore.load();
		await loadEngines();
		if (engines.length > 0) await loadActive();
	});

	onDestroy(stopPolls);

	// What a query used (#407): the list's figures plus what the engine
	// reported when the detail was opened. Unknown figures are left out.
	function queryUsage(q: MonitoringQueryDetail): { label: string; value: string }[] {
		const r = q.resources;
		const rows = (n: number) => n.toLocaleString();
		const items: [string, number | null | undefined, (n: number) => string][] = [
			[m.monitoring_cpu_time(), q.cpu_ms, formatDuration],
			[m.monitoring_elapsed(), q.elapsed_ms, formatDuration],
			[m.monitoring_queued(), r?.queued_ms, formatDuration],
			[m.monitoring_planning(), r?.planning_ms, formatDuration],
			[m.monitoring_peak_memory(), r?.peak_memory_bytes, formatBytes],
			[m.monitoring_data_read(), q.physical_input_bytes, formatBytes],
			[m.monitoring_rows_processed(), r?.processed_rows, rows],
			[m.monitoring_bytes_processed(), r?.processed_bytes, formatBytes],
			[m.monitoring_rows_returned(), r?.output_rows ?? q.output_rows, rows],
			[m.monitoring_bytes_returned(), r?.output_bytes, formatBytes]
		];
		return items
			.filter((item): item is [string, number, (n: number) => string] => item[1] != null)
			.map(([label, value, format]) => ({ label, value: format(value) }));
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_monitoring()}</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{m.monitoring_cluster_load_running_queries_and_iceberg_t()}
			</p>
		</div>
		{#if engines.length > 1}
			<div class="w-56">
				<Select
					label={m.common_engine()}
					bind:value={engineId}
					options={engines.map((engine) => ({ value: engine.id, label: engine.name }))}
					onchange={() => loadActive()}
				/>
			</div>
		{/if}
	</div>

	{#if loading}
		<div
			class="flex flex-col gap-2"
			role="status"
			aria-label={m.monitoring_loading_monitoring_engines()}
		>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-14 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState
			title={m.monitoring_could_not_load_monitoring()}
			message={error}
			onretry={loadEngines}
		/>
	{:else if engines.length === 0}
		<EmptyState
			title={m.monitoring_no_engine_with_monitoring_support()}
			description={m.monitoring_monitoring_is_available_for_federated_engines()}
		/>
	{:else}
		<div
			class="mb-4 flex gap-1 border-b border-border"
			role="tablist"
			aria-label={m.monitoring_monitoring_tabs()}
		>
			{#each [['cluster', m.monitoring_cluster()], ['queries', 'Queries'], ['tables', m.monitoring_table_health()]] as [value, label] (value)}
				<button
					type="button"
					role="tab"
					aria-selected={tab === value}
					class="-mb-px border-b-2 px-3 py-1.5 text-[12px] font-semibold {tab === value
						? 'border-primary text-primary'
						: 'border-transparent text-slate-600 hover:text-text-main dark:text-slate-300'}"
					onclick={() => selectTab(value as 'cluster' | 'queries' | 'tables')}
				>
					{label}
				</button>
			{/each}
		</div>

		{#if tab === 'cluster'}
			<div class="mb-3 flex items-center gap-2">
				<div class="w-40">
					<Select
						label={m.monitoring_window()}
						bind:value={clusterWindow}
						options={[
							{ value: '1h', label: m.monitoring_last_hour() },
							{ value: '6h', label: m.monitoring_last_6_hours() },
							{ value: '24h', label: m.monitoring_last_24_hours() }
						]}
						onchange={() => loadCluster()}
					/>
				</div>
				{#if cluster?.latest}
					<span class="text-[11px] text-slate-600 dark:text-slate-300">
						{m.monitoring_updated()}
						<RelativeTime at={cluster.latest.captured_at} live />
					</span>
				{/if}
			</div>
			{#if clusterLoading && !cluster}
				<Skeleton class="h-32 w-full" />
			{:else if clusterError}
				<ErrorState
					title={m.monitoring_could_not_load_the_cluster()}
					message={clusterError}
					onretry={loadCluster}
				/>
			{:else if !cluster?.latest}
				<EmptyState
					title={m.monitoring_no_cluster_snapshot()}
					description={m.monitoring_the_poller_has_not_captured_this()}
				/>
			{:else}
				<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
					{#each [['Workers', cluster.latest.active_workers], ['Running', cluster.latest.running_queries], ['Queued', cluster.latest.queued_queries], ['Blocked', cluster.latest.blocked_queries]] as [label, value] (label)}
						<div class="rounded-lg border border-border bg-bg-panel p-3">
							<div class="text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300">
								{label}
							</div>
							<div class="mt-1 text-xl font-semibold">{value}</div>
						</div>
					{/each}
					<div class="rounded-lg border border-border bg-bg-panel p-3">
						<div class="text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300">
							{m.monitoring_memory_used()}
						</div>
						<div class="mt-1 text-xl font-semibold">
							{cluster.latest.total_memory_bytes && cluster.latest.free_memory_bytes
								? `${Math.round(
										(1 - cluster.latest.free_memory_bytes / cluster.latest.total_memory_bytes) * 100
									)}%`
								: '—'}
						</div>
					</div>
				</div>
				{#if cluster.series.length > 1}
					<QueriesChart series={cluster.series} />
				{/if}
			{/if}
		{:else if tab === 'queries'}
			<div
				class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-4"
			>
				<Select
					label={m.monitoring_scope()}
					bind:value={scope}
					options={[
						{ value: 'mine', label: 'Mine' },
						...(isAdmin ? [{ value: 'all', label: m.monitoring_everyone() }] : [])
					]}
					onchange={() => loadQueries()}
				/>
				<Select
					label={m.monitoring_state()}
					bind:value={queryState}
					options={[
						{ value: '', label: m.monitoring_any() },
						{ value: 'running', label: 'Running' },
						{ value: 'finished', label: 'Finished' },
						{ value: 'failed', label: 'Failed' },
						{ value: 'cancelled', label: 'Cancelled' }
					]}
					onchange={() => loadQueries()}
				/>
				<Input label={m.monitoring_sql_contains()} bind:value={queryText} placeholder="orders" />
				<div class="flex items-end">
					<Button onclick={() => loadQueries()}>{m.monitoring_search()}</Button>
				</div>
			</div>
			{#if queriesLoading && queryPage.items.length === 0}
				<Skeleton class="h-32 w-full" />
			{:else if queriesError}
				<ErrorState
					title={m.monitoring_could_not_load_queries()}
					message={queriesError}
					onretry={loadQueries}
				/>
			{:else if queryPage.items.length === 0}
				<EmptyState
					title={m.monitoring_no_queries()}
					description={m.monitoring_no_query_matches_this_engine_state()}
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">{m.monitoring_state()}</th>
								<th class="px-3 py-2">{m.monitoring_user()}</th>
								<th class="px-3 py-2">SQL</th>
								<th class="px-3 py-2">{m.monitoring_catalog()}</th>
								<th class="px-3 py-2">{m.monitoring_elapsed()}</th>
								<th class="px-3 py-2">{m.monitoring_cpu()}</th>
								<th class="px-3 py-2">{m.monitoring_started()}</th>
								<th class="px-3 py-2 text-right">{m.monitoring_actions()}</th>
							</tr>
						</thead>
						<tbody>
							{#each queryPage.items as query (query.trino_query_id)}
								<tr
									class="cursor-pointer border-b border-border/60 last:border-b-0 hover:bg-bg-toolbar"
									onclick={() => openQuery(query)}
									onkeydown={(e) => e.key === 'Enter' && openQuery(query)}
									tabindex="0"
								>
									<td class="px-3 py-1.5">
										<StatusBadge
											kind="execution"
											status={query.state === 'running'
												? 'running'
												: query.state === 'finished'
													? 'finished'
													: query.state === 'failed'
														? 'failed'
														: 'cancelled'}
										/>
									</td>
									<td class="px-3 py-1.5 font-mono">{query.user}</td>
									<td
										class="max-w-[320px] truncate px-3 py-1.5 font-mono"
										title={query.sql_first_line}
									>
										{query.sql_first_line}
									</td>
									<td class="px-3 py-1.5">{query.catalog}</td>
									<td class="px-3 py-1.5">
										{query.elapsed_ms != null ? formatDuration(query.elapsed_ms) : '—'}
									</td>
									<td class="px-3 py-1.5">
										{query.cpu_ms != null ? formatDuration(query.cpu_ms) : '—'}
									</td>
									<td class="px-3 py-1.5"><RelativeTime at={query.created_at} /></td>
									<td class="px-3 py-1.5 text-right">
										{#if query.state === 'running'}
											<Button
												size="sm"
												variant="danger"
												onclick={(event) => {
													event.stopPropagation();
													askKill(query);
												}}
											>
												{m.monitoring_kill()}
											</Button>
										{:else}
											<span class="text-slate-600 dark:text-slate-300">—</span>
										{/if}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{:else}
			<div
				class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-4"
			>
				<Select
					label={m.monitoring_catalog()}
					bind:value={catalogId}
					options={catalogs.map((catalog) => ({ value: catalog.id, label: catalog.name }))}
					onchange={() => loadHealth()}
				/>
				<Select
					label={m.monitoring_needs()}
					bind:value={needs}
					options={[
						{ value: '', label: m.monitoring_any() },
						{ value: 'compaction', label: m.monitoring_compaction() },
						{ value: 'snapshot_expiry', label: m.monitoring_snapshot_expiry_2() },
						{ value: 'orphan_cleanup', label: m.monitoring_orphan_cleanup_2() }
					]}
					onchange={() => loadHealth()}
				/>
				<Input label={m.monitoring_max_health_score()} bind:value={maxScore} placeholder="80" />
				<div class="flex items-end">
					<Button onclick={() => loadHealth()}>{m.monitoring_refresh()}</Button>
				</div>
			</div>
			{#if catalogs.length === 0}
				<EmptyState
					title={m.monitoring_no_catalogs()}
					description={m.monitoring_no_iceberg_catalog_is_granted_to()}
				/>
			{:else if healthLoading && health.length === 0}
				<Skeleton class="h-32 w-full" />
			{:else if healthError}
				<ErrorState
					title={m.monitoring_could_not_load_table_health()}
					message={healthError}
					onretry={loadHealth}
				/>
			{:else if health.length === 0}
				<EmptyState
					title={m.monitoring_no_table_health_rows()}
					description={m.monitoring_the_health_poller_has_not_seen()}
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">{m.monitoring_table()}</th>
								<th class="px-3 py-2">{m.monitoring_health()}</th>
								<th class="px-3 py-2">{m.monitoring_snapshots()}</th>
								<th class="px-3 py-2">{m.monitoring_files()}</th>
								<th class="px-3 py-2">{m.monitoring_size()}</th>
								<th class="px-3 py-2">{m.monitoring_oldest()}</th>
								<th class="px-3 py-2">{m.monitoring_needs()}</th>
								<th class="px-3 py-2">{m.monitoring_last_maintenance()}</th>
								<th class="px-3 py-2 text-right">{m.monitoring_actions()}</th>
							</tr>
						</thead>
						<tbody>
							{#each health as row (row.catalog_id + row.namespace + row.table_name)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-3 py-1.5 font-mono">{row.namespace}.{row.table_name}</td>
									<td class="px-3 py-1.5">
										<div class="flex items-center gap-1.5">
											<Badge tone={scoreTone(row.health_score)}>{row.health_score}</Badge>
											<div class="h-1.5 w-16 overflow-hidden rounded bg-border" aria-hidden="true">
												<div
													class="h-full {scoreTone(row.health_score) === 'success'
														? 'bg-success'
														: scoreTone(row.health_score) === 'warning'
															? 'bg-amber-500'
															: 'bg-danger'}"
													style="width: {row.health_score}%"
												></div>
											</div>
										</div>
									</td>
									<td class="px-3 py-1.5">{row.snapshot_count}</td>
									<td class="px-3 py-1.5">{row.file_count ?? '—'}</td>
									<td class="px-3 py-1.5">
										{row.total_size_bytes != null
											? m.monitoring_mib({ value: Math.round(row.total_size_bytes / 1024 / 1024) })
											: '—'}
									</td>
									<td class="px-3 py-1.5">
										{row.oldest_snapshot_age_hours != null
											? m.monitoring_hours({ hours: row.oldest_snapshot_age_hours })
											: '—'}
									</td>
									<td class="px-3 py-1.5">
										<div class="flex flex-wrap gap-1">
											{#each needsLabels(row) as label (label)}
												<Badge tone="warning">{label}</Badge>
											{/each}
										</div>
									</td>
									<td class="px-3 py-1.5">
										{#if row.last_maintenance_at}
											<RelativeTime at={row.last_maintenance_at} />
										{:else}
											—
										{/if}
									</td>
									<td class="px-3 py-1.5 text-right">
										<Button size="sm" onclick={() => openMaintenance(row)}
											>{m.monitoring_run_maintenance_2()}</Button
										>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
			{#if catalogs.length > 0 && !healthError && !(healthLoading && health.length === 0)}
				<h2 class="mt-5 text-[13px] font-semibold">{m.monitoring_maintenance_runs()}</h2>
				<div class="mt-2 overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">{m.monitoring_status()}</th>
								<th class="px-3 py-2">{m.monitoring_table()}</th>
								<th class="px-3 py-2">{m.monitoring_operations()}</th>
								<th class="px-3 py-2">{m.monitoring_started()}</th>
								<th class="px-3 py-2">{m.monitoring_finished()}</th>
								<th class="px-3 py-2">{m.monitoring_error()}</th>
							</tr>
						</thead>
						<tbody>
							{#each runs as run (run.id)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-3 py-1.5">
										<StatusBadge kind="maintenance" status={run.status} />
									</td>
									<td class="px-3 py-1.5 font-mono">{run.namespace}.{run.table_name}</td>
									<td class="px-3 py-1.5">{run.operations.join(', ')}</td>
									<td class="px-3 py-1.5"><RelativeTime at={run.started_at} /></td>
									<td class="px-3 py-1.5">
										{#if run.finished_at}
											<RelativeTime at={run.finished_at} />
										{:else}
											—
										{/if}
									</td>
									<td class="px-3 py-1.5">
										{#if run.error_code}
											<span class="font-mono text-danger">{run.error_code}</span>
										{:else}
											—
										{/if}
									</td>
								</tr>
							{:else}
								<tr>
									<td class="px-3 py-3 text-slate-600 dark:text-slate-300" colspan="6">
										{m.monitoring_no_maintenance_runs_yet()}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{/if}
	{/if}
</div>

<Drawer size="lg" bind:open={queryDrawer} title={m.monitoring_query_detail()}>
	{#if queryLoading}
		<div class="flex flex-col gap-2" role="status" aria-label={m.monitoring_loading_query_detail()}>
			<Skeleton class="h-4 w-2/3" />
			<Skeleton class="h-4 w-1/2" />
			<Skeleton class="h-24 w-full" />
		</div>
	{:else if queryError}
		<ErrorState
			title={m.monitoring_could_not_load_the_query()}
			message={queryError}
			onretry={() => queryOpening && openQuery(queryOpening)}
		/>
	{:else if selectedQuery}
		<dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
			<dt class="text-slate-600 dark:text-slate-300">{m.monitoring_state()}</dt>
			<dd>{selectedQuery.state}</dd>
			<dt class="text-slate-600 dark:text-slate-300">{m.common_user()}</dt>
			<dd class="font-mono">{selectedQuery.user}</dd>
			<dt class="text-slate-600 dark:text-slate-300">{m.common_catalog()}</dt>
			<dd class="font-mono">{selectedQuery.catalog}</dd>
			<dt class="text-slate-600 dark:text-slate-300">{m.monitoring_started()}</dt>
			<dd><RelativeTime at={selectedQuery.created_at} live /></dd>
			{#if selectedQuery.execution_id}
				<dt class="text-slate-600 dark:text-slate-300">{m.monitoring_execution()}</dt>
				<dd>
					<button
						type="button"
						class="text-primary hover:underline"
						onclick={() => {
							if (selectedQuery) {
								window.location.href = `/history?execution=${selectedQuery.execution_id}`;
							}
						}}
					>
						{selectedQuery.execution_id}
					</button>
				</dd>
			{/if}
			<dt class="text-slate-600 dark:text-slate-300">{m.monitoring_query_id()}</dt>
			<dd class="flex items-center gap-1 break-all font-mono">
				{selectedQuery.trino_query_id}<CopyButton
					value={selectedQuery.trino_query_id}
					label={m.monitoring_query_id_2()}
				/>
			</dd>
		</dl>
		{@const usage = queryUsage(selectedQuery)}
		{#if usage.length}
			<h3 class="mt-4 text-[12px] font-semibold">{m.monitoring_resources()}</h3>
			<dl
				class="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]"
				aria-label={m.monitoring_resources()}
			>
				{#each usage as item (item.label)}
					<dt class="text-slate-600 dark:text-slate-300">{item.label}</dt>
					<dd class="font-mono">{item.value}</dd>
				{/each}
			</dl>
		{/if}
		<h3 class="mt-4 text-[12px] font-semibold">{m.common_sql()}</h3>
		<pre
			class="mt-1 overflow-x-auto rounded-lg border border-border bg-bg-toolbar p-3 font-mono text-[11px]">{selectedQuery.sql}</pre>
		{#if selectedQuery.error_message}
			<div class="mt-3">
				<ErrorPanel error={{ code: 'engine_error', message: selectedQuery.error_message }} />
			</div>
		{/if}
	{/if}
</Drawer>

<Modal bind:open={maintainOpen} title={m.monitoring_run_iceberg_maintenance()}>
	{#if maintainTarget}
		<div class="flex flex-col gap-3 text-[12px]">
			<p class="font-mono">
				{maintainTarget.catalog_name}.{maintainTarget.namespace}.{maintainTarget.table_name}
			</p>
			<Checkbox
				label={m.monitoring_optimize_rewrite_small_files()}
				bind:checked={operations.optimize}
			/>
			<Checkbox
				label={m.monitoring_expire_snapshots_time_travel_to_them_stops()}
				bind:checked={operations.expire_snapshots}
			/>
			<Checkbox
				label={m.monitoring_remove_orphan_files_files_no_snapshot_refe()}
				bind:checked={operations.remove_orphan_files}
			/>
			<!-- Retention only applies to expiry and orphan cleanup (#481); the value
			     is kept while the field is hidden. -->
			{#if operations.expire_snapshots || operations.remove_orphan_files}
				<div class="flex flex-col gap-1">
					<Input
						label={m.monitoring_retention_e_g_7d_30d()}
						bind:value={retention}
						class="font-mono"
					/>
					<p class="text-[11px] text-slate-600 dark:text-slate-300">
						{m.monitoring_retention_applies_to()}
					</p>
				</div>
			{/if}
			{#if maintainError}
				<p class="text-danger" role="alert">{maintainError}</p>
			{/if}
		</div>
	{/if}
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (maintainOpen = false)} disabled={maintaining}
				>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				loading={maintaining}
				disabled={maintaining}
				onclick={submitMaintenance}
			>
				{m.monitoring_run_maintenance()}
			</Button>
		</div>
	{/snippet}
</Modal>

{#if killing}
	<ConfirmDialog
		bind:open={killOpen}
		title={m.monitoring_kill_this_query()}
		resourceName={killing.sql_first_line || killing.trino_query_id}
		consequences={[
			m.monitoring_kill_consequence_stop({
				query: killing.sql_first_line || killing.trino_query_id,
				engine: engines.find((engine) => engine.id === engineId)?.name ?? engineId,
				user: killing.user
			}),
			m.monitoring_kill_consequence_undo()
		]}
		confirmLabel={m.monitoring_kill_query()}
		danger
		onconfirm={kill}
	/>
{/if}
