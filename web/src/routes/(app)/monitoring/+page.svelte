<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onDestroy, onMount } from 'svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { loadCatalogs, type MetadataCatalog } from '$lib/metadata/client';
	import { poll, type PollController } from '$lib/util/poll';
	import { formatDuration } from '$lib/util/time';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
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
	let rounds = $state<MaintenanceRun[]>([]);
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

	async function loadHealth() {
		if (!catalogId) return;
		healthLoading = true;
		healthError = '';
		try {
			health = await listHealth(catalogId, {
				needs: needs || undefined,
				maxScore: maxScore ? Number(maxScore) : undefined
			});
			runs = await listMaintenanceRuns(catalogId);
		} catch (e) {
			healthError = e instanceof Error ? e.message : String(e);
		} finally {
			healthLoading = false;
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

	async function openQuery(query: MonitoringQuery) {
		if (!engineId) return;
		try {
			selectedQuery = await getQuery(engineId, query.trino_query_id);
			queryDrawer = true;
		} catch {
			selectedQuery = null;
		}
	}

	async function kill(query: MonitoringQuery) {
		if (!engineId) return;
		try {
			await killQuery(engineId, query.trino_query_id);
			await loadQueries();
		} catch (e) {
			queriesError = e instanceof Error ? e.message : String(e);
		}
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
		if (!catalogId || !maintainTarget) return;
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
			const run = await createMaintenance(catalogId, {
				namespace: maintainTarget.namespace,
				table: maintainTarget.table_name,
				operations: selected,
				retention: selected.some((op) => op !== 'optimize') ? retention : undefined
			});
			maintainOpen = false;
			runs = [run, ...runs];
			const controller = poll(
				async () => {
					const current = await listMaintenanceRuns(catalogId);
					runs = current;
					return current.find((item) => item.id === run.id);
				},
				{ until: (item) => !!item && item.status !== 'running', intervals: [1000, 2000, 5000] }
			);
			await controller.done;
			await loadHealth();
		} catch (e) {
			maintainError = e instanceof Error ? e.message : String(e);
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
		<ErrorState title="Could not load monitoring" message={error} onretry={loadEngines} />
	{:else if engines.length === 0}
		<EmptyState
			title="No engine with monitoring support"
			description="Monitoring is available for federated engines such as Trino."
		/>
	{:else}
		<div class="mb-4 flex gap-1 border-b border-border" role="tablist" aria-label="Monitoring tabs">
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
						label="Window"
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
						updated <RelativeTime at={cluster.latest.captured_at} live />
					</span>
				{/if}
			</div>
			{#if clusterLoading && !cluster}
				<Skeleton class="h-32 w-full" />
			{:else if clusterError}
				<ErrorState
					title="Could not load the cluster"
					message={clusterError}
					onretry={loadCluster}
				/>
			{:else if !cluster?.latest}
				<EmptyState
					title="No cluster snapshot"
					description="The poller has not captured this engine yet."
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
							Memory used
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
					{@const max = Math.max(1, ...cluster.series.map((point) => point.running_queries))}
					<div class="mt-4 rounded-lg border border-border bg-bg-panel p-3">
						<p class="text-[11px] font-semibold">Running queries</p>
						<svg
							viewBox="0 0 300 60"
							class="mt-2 h-20 w-full"
							role="img"
							aria-label="Running queries over time"
						>
							<polyline
								fill="none"
								stroke="currentColor"
								stroke-width="2"
								class="text-primary"
								points={cluster.series
									.map(
										(point, index) =>
											`${(index / (cluster!.series.length - 1)) * 300},${60 - (point.running_queries / max) * 55}`
									)
									.join(' ')}
							/>
						</svg>
					</div>
				{/if}
			{/if}
		{:else if tab === 'queries'}
			<div
				class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-4"
			>
				<Select
					label="Scope"
					bind:value={scope}
					options={[
						{ value: 'mine', label: 'Mine' },
						...(isAdmin ? [{ value: 'all', label: m.monitoring_everyone() }] : [])
					]}
					onchange={() => loadQueries()}
				/>
				<Select
					label="State"
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
				<Input label="SQL contains" bind:value={queryText} placeholder="orders" />
				<div class="flex items-end">
					<Button onclick={() => loadQueries()}>Search</Button>
				</div>
			</div>
			{#if queriesLoading && queryPage.items.length === 0}
				<Skeleton class="h-32 w-full" />
			{:else if queriesError}
				<ErrorState title="Could not load queries" message={queriesError} onretry={loadQueries} />
			{:else if queryPage.items.length === 0}
				<EmptyState
					title="No queries"
					description="No query matches this engine, state and text."
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">State</th>
								<th class="px-3 py-2">User</th>
								<th class="px-3 py-2">SQL</th>
								<th class="px-3 py-2">Catalog</th>
								<th class="px-3 py-2">Elapsed</th>
								<th class="px-3 py-2">Started</th>
								<th class="px-3 py-2 text-right">Actions</th>
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
									<td class="px-3 py-1.5"><RelativeTime at={query.created_at} /></td>
									<td class="px-3 py-1.5 text-right">
										{#if query.state === 'running'}
											<Button
												size="sm"
												variant="danger"
												onclick={(event) => {
													event.stopPropagation();
													void kill(query);
												}}
											>
												Kill
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
					label="Catalog"
					bind:value={catalogId}
					options={catalogs.map((catalog) => ({ value: catalog.id, label: catalog.name }))}
					onchange={() => loadHealth()}
				/>
				<Select
					label="Needs"
					bind:value={needs}
					options={[
						{ value: '', label: m.monitoring_any() },
						{ value: 'compaction', label: m.monitoring_compaction() },
						{ value: 'snapshot_expiry', label: m.monitoring_snapshot_expiry_2() },
						{ value: 'orphan_cleanup', label: m.monitoring_orphan_cleanup_2() }
					]}
					onchange={() => loadHealth()}
				/>
				<Input label="Max health score" bind:value={maxScore} placeholder="80" />
				<div class="flex items-end">
					<Button onclick={() => loadHealth()}>Refresh</Button>
				</div>
			</div>
			{#if catalogs.length === 0}
				<EmptyState
					title="No catalogs"
					description="No Iceberg catalog is granted to this workspace."
				/>
			{:else if healthLoading && health.length === 0}
				<Skeleton class="h-32 w-full" />
			{:else if healthError}
				<ErrorState
					title="Could not load table health"
					message={healthError}
					onretry={loadHealth}
				/>
			{:else if health.length === 0}
				<EmptyState
					title="No table health rows"
					description="The health poller has not seen a table in this catalog yet."
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">Table</th>
								<th class="px-3 py-2">Health</th>
								<th class="px-3 py-2">Snapshots</th>
								<th class="px-3 py-2">Files</th>
								<th class="px-3 py-2">Size</th>
								<th class="px-3 py-2">Oldest</th>
								<th class="px-3 py-2">Needs</th>
								<th class="px-3 py-2">Last maintenance</th>
								<th class="px-3 py-2 text-right">Actions</th>
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
											? `${Math.round(row.total_size_bytes / 1024 / 1024)} MiB`
											: '—'}
									</td>
									<td class="px-3 py-1.5">
										{row.oldest_snapshot_age_hours != null
											? `${row.oldest_snapshot_age_hours}h`
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
										<Button size="sm" onclick={() => openMaintenance(row)}>Run maintenance…</Button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>

				<h2 class="mt-5 text-[13px] font-semibold">Maintenance runs</h2>
				<div class="mt-2 overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">Status</th>
								<th class="px-3 py-2">Table</th>
								<th class="px-3 py-2">Operations</th>
								<th class="px-3 py-2">Started</th>
								<th class="px-3 py-2">Finished</th>
								<th class="px-3 py-2">Error</th>
							</tr>
						</thead>
						<tbody>
							{#each rounds as run (run.id)}
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
										No maintenance runs yet.
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

<Drawer bind:open={queryDrawer} title={m.monitoring_query_detail()}>
	{#if selectedQuery}
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
			<p class="font-mono">{maintainTarget.namespace}.{maintainTarget.table_name}</p>
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
			<Input label={m.monitoring_retention_e_g_7d_30d()} bind:value={retention} class="font-mono" />
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
