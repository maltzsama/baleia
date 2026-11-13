<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import TimeSeriesChart from '$lib/monitoring/TimeSeriesChart.svelte';
	import Sparkline from '$lib/monitoring/Sparkline.svelte';
	import { downloadCsv, toCsv } from '$lib/util/csv';
	import { onDestroy, onMount } from 'svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { loadCatalogs, type MetadataCatalog } from '$lib/metadata/client';
	import { resolve } from '$app/paths';
	import { goto } from '$app/navigation';
	import { analyzeCatalogTableHealth, runTableMaintenance } from '$lib/maintenance/client';
	import TableMaintenance from '$lib/maintenance/TableMaintenance.svelte';
	import CatalogMaintenancePlan from '$lib/maintenance/CatalogMaintenancePlan.svelte';
	import { operationLabel, triggerLabel } from '$lib/maintenance/labels';
	import { poll, type PollController } from '$lib/util/poll';
	import { streamEvents } from '$lib/assistant/sse';
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
		type ClusterPoint,
		type ClusterSeries,
		type MaintenanceRun,
		type MonitoringEngine,
		type MonitoringQuery,
		type MonitoringQueryDetail,
		type MonitoringQueryPage,
		type TableHealth
	} from '$lib/monitoring/client';
	import { engineHealth, memoryUsedPercent, type EngineHealth } from '$lib/monitoring/health';
	import { getEngineQueues, type EngineQueue } from '$lib/workload/admin';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const isAdmin = $derived(authStore.user?.role === 'admin');

	let engines = $state<MonitoringEngine[]>([]);
	let engineId = $state('');
	let tab = $state<'cluster' | 'queries' | 'tables' | 'maintenance'>('cluster');
	let loading = $state(true);
	let error = $state('');

	let windowPreset = $state('1h');
	let customWindow = $state('90m');
	// The API accepts a Go duration (90m, 36h) or whole days (7d); the presets
	// are shortcuts over the same value.
	const clusterWindow = $derived(
		windowPreset === 'custom' ? customWindow.trim() || '1h' : windowPreset
	);
	let cluster = $state<ClusterSeries | null>(null);
	let clusterLoading = $state(false);
	let clusterError = $state('');
	// Live updates arrive over SSE (#733) with a slow poll as the fallback;
	// pausing stops both, and Refresh still pulls one snapshot on demand.
	let live = $state(true);

	// One card per engine: its latest snapshot, the health that snapshot
	// implies, its running-query trend, and (for an admin or the owner) its
	// queue classes.
	type EngineOverview = {
		engine: MonitoringEngine;
		latest: ClusterPoint | null;
		history: number[];
		classes: EngineQueue[];
	};
	let overview = $state<EngineOverview[]>([]);
	let overviewLoading = $state(false);
	let overviewSeq = 0;

	async function loadOverview() {
		// The overview is only shown (and fetched) with more than one engine.
		if (engines.length < 2) return;
		const seq = ++overviewSeq;
		overviewLoading = true;
		const rows = await Promise.all(
			engines.map(async (engine) => {
				try {
					const [series, queues] = await Promise.all([
						getCluster(engine.id, clusterWindow),
						// Queue capacities are admin/owner only; a member still gets the card.
						getEngineQueues(engine.id).catch(() => null)
					]);
					return {
						engine,
						latest: series.latest ?? null,
						history: series.series.map((point) => point.running_queries),
						classes: queues?.classes ?? []
					};
				} catch {
					return { engine, latest: null, history: [], classes: [] };
				}
			})
		);
		if (seq !== overviewSeq) return;
		overview = rows;
		overviewLoading = false;
	}

	function selectEngine(id: string) {
		if (id === engineId) return;
		engineId = id;
		void loadActive();
	}

	function healthTone(health: EngineHealth): 'success' | 'warning' | 'danger' {
		return health === 'ok' ? 'success' : health === 'degraded' ? 'warning' : 'danger';
	}

	function healthLabel(health: EngineHealth): string {
		return health === 'ok'
			? m.monitoring_health_ok()
			: health === 'degraded'
				? m.monitoring_health_degraded()
				: m.monitoring_health_critical();
	}

	function classLabel(name: string): string {
		switch (name) {
			case 'interactive':
				return m.workload_class_interactive();
			case 'heavy':
				return m.workload_class_heavy();
			case 'background':
				return m.workload_class_background();
		}
		return name;
	}

	// The chart takes series of `{ t, v }`; the cluster snapshot keeps the
	// capture in `captured_at` (#820). Running, queued and blocked share the
	// same capture, so they read on one axis.
	const clusterSeries = $derived(
		cluster
			? [
					{
						key: 'running',
						label: m.monitoring_running(),
						points: cluster.series.map((point) => ({
							t: point.captured_at,
							v: point.running_queries
						}))
					},
					{
						key: 'queued',
						label: m.monitoring_queued(),
						points: cluster.series.map((point) => ({
							t: point.captured_at,
							v: point.queued_queries
						}))
					},
					{
						key: 'blocked',
						label: m.monitoring_blocked(),
						points: cluster.series.map((point) => ({
							t: point.captured_at,
							v: point.blocked_queries
						}))
					}
				]
			: []
	);

	let queryPage = $state<MonitoringQueryPage>({ items: [] });
	let queriesLoading = $state(false);
	let queriesError = $state('');
	let exportingQueries = $state(false);
	let scope = $state('mine');
	let queryState = $state('');
	let queryText = $state('');
	let querySort = $state<'created' | 'elapsed' | 'cpu' | 'memory'>('created');
	// Top-N by duration, CPU or memory is a sort of the page the API returned.
	const compareQueries = (a: MonitoringQuery, b: MonitoringQuery) => {
		if (querySort === 'elapsed') return (b.elapsed_ms ?? -1) - (a.elapsed_ms ?? -1);
		if (querySort === 'cpu') return (b.cpu_ms ?? -1) - (a.cpu_ms ?? -1);
		if (querySort === 'memory') return (b.peak_memory_bytes ?? -1) - (a.peak_memory_bytes ?? -1);
		return b.created_at.localeCompare(a.created_at);
	};
	const sortedQueries = $derived([...queryPage.items].sort(compareQueries));
	let selectedQuery = $state<MonitoringQueryDetail | null>(null);
	let queryDrawer = $state(false);

	let catalogs = $state<MetadataCatalog[]>([]);
	let catalogId = $state('');
	// Read-only catalogs are analyzed, never maintained (#996).
	const isReadOnly = (id: string) =>
		catalogs.find((catalog) => catalog.id === id)?.read_only ?? false;
	const selectedReadOnly = $derived(isReadOnly(catalogId));
	let health = $state<TableHealth[]>([]);
	let healthLoading = $state(false);
	let healthError = $state('');
	let healthNamespace = $state('');
	let needs = $state('');
	let maxScore = $state('');
	let runs = $state<MaintenanceRun[]>([]);
	let runTrigger = $state('');
	let runOperation = $state('');
	let analyzing = $state(false);
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
	// The per-table maintenance editor (#820) opens in a drawer from the
	// health and maintenance tabs; it reuses the full page component.
	let editor = $state<{ catalogId: string; namespace: string; table: string } | null>(null);
	let editorOpen = $state(false);

	let polls: PollController<unknown>[] = [];
	// The live SSE subscription (#733): the server pushes a "changed" signal
	// instead of the client polling on a timer.
	let eventsAbort: AbortController | null = null;
	// Tab and engine changes overlap async loads; only the latest one may
	// start its polls and stream.
	let activeSeq = 0;

	function stopPolls() {
		for (const controller of polls) controller.stop();
		polls = [];
	}

	function stopEvents() {
		eventsAbort?.abort();
		eventsAbort = null;
	}

	function startEvents() {
		stopEvents();
		if (!engineId || (tab !== 'cluster' && tab !== 'queries')) return;
		const controller = new AbortController();
		eventsAbort = controller;
		const url = `/engines/${encodeURIComponent(engineId)}/monitoring/events`;
		const refresh = () => {
			if (tab === 'queries') void loadQueries();
			else if (tab === 'cluster') void loadCluster();
		};
		void (async () => {
			let attempts = 0;
			while (!controller.signal.aborted) {
				try {
					await streamEvents(url, {
						signal: controller.signal,
						onOpen: () => {
							attempts = 0;
						},
						onEvent: (frame) => {
							if (frame.event !== 'changed') return;
							let kind: string;
							try {
								kind = (JSON.parse(frame.data) as { kind?: string }).kind ?? '';
							} catch {
								return;
							}
							if (kind === 'queries' && tab === 'queries') void loadQueries();
							else if (kind === 'cluster' && tab === 'cluster') void loadCluster();
						}
					});
				} catch {
					// A rejected connection is retried below.
				}
				if (controller.signal.aborted) return;
				attempts += 1;
				const delay = Math.min(30_000, 500 * 2 ** Math.min(attempts, 6));
				await new Promise((resolve) => setTimeout(resolve, delay));
				if (controller.signal.aborted) return;
				// Catch up on what changed while the stream was down.
				refresh();
			}
		})();
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

	async function loadQueries(): Promise<MonitoringQueryPage> {
		if (!engineId) return queryPage;
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
		return queryPage;
	}

	async function loadCatalogList() {
		if (!workspaceId || catalogs.length > 0) return;
		try {
			// Table health covers the catalogs the server marks (#500, #996).
			catalogs = (await loadCatalogs(workspaceId)).filter((catalog) => catalog.table_health);
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
				ns: healthNamespace.trim() || undefined,
				needs: needs || undefined,
				maxScore: maxScore ? Number(maxScore) : undefined
			});
			const list = await listMaintenanceRuns(target, runFilter());
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
		const seq = ++activeSeq;
		stopPolls();
		stopEvents();
		if (!engineId && tab !== 'tables' && tab !== 'maintenance') return;
		if (tab === 'cluster') {
			void loadOverview();
			await loadCluster();
			if (seq !== activeSeq || !live) return;
			// Push is the primary path (#733); this slow poll is the safety net.
			polls.push(
				poll(loadCluster, { until: () => false, intervals: [60_000], maxDurationMs: 3_600_000 })
			);
			startEvents();
		} else if (tab === 'queries') {
			await loadQueries();
			if (seq !== activeSeq || !live) return;
			polls.push(
				poll(loadQueries, {
					until: () => false,
					// Fallback only: live updates arrive over SSE. Stay responsive
					// while queries run, back off when the list is idle.
					intervalFor: (page) =>
						page?.items.some((query) => query.state === 'running' || query.state === 'queued')
							? 20_000
							: 60_000,
					maxDurationMs: 3_600_000
				})
			);
			startEvents();
		} else if (tab === 'tables') {
			await loadCatalogList();
			if (seq !== activeSeq) return;
			// The first tick loads the rows and keeps watching for the hourly
			// pass (#750); don't also await loadHealth or it double-fetches.
			polls.push(
				poll(loadHealth, { until: () => false, intervals: [60_000], maxDurationMs: 3_600_000 })
			);
		} else {
			// The Maintenance tab only needs the catalog list; the plan loads
			// in the component itself.
			await loadCatalogList();
		}
	}

	function setLive(next: boolean) {
		if (next === live) return;
		live = next;
		if (live) void loadActive();
		else {
			stopPolls();
			stopEvents();
		}
	}

	function refreshActive() {
		if (tab === 'cluster') {
			void loadOverview();
			void loadCluster();
		} else if (tab === 'queries') {
			void loadQueries();
		}
	}

	function exportHealth() {
		try {
			downloadCsv(
				'iceberg-health.csv',
				toCsv(
					[
						m.monitoring_schema(),
						m.monitoring_table(),
						m.monitoring_health(),
						m.monitoring_snapshots(),
						m.monitoring_files(),
						m.maintenance_col_delete_files(),
						m.maintenance_col_commits_hour(),
						m.monitoring_size_bytes(),
						m.monitoring_oldest_hours(),
						m.monitoring_needs(),
						m.maintenance_mode(),
						m.monitoring_last_maintenance(),
						`${m.maintenance_op_optimize()} ${m.maintenance_col_last_run()}`,
						`${m.maintenance_op_optimize_manifests()} ${m.maintenance_col_last_run()}`,
						`${m.maintenance_op_expire_snapshots()} ${m.maintenance_col_last_run()}`,
						`${m.maintenance_op_remove_orphan_files()} ${m.maintenance_col_last_run()}`
					],
					health.map((row) => [
						row.namespace,
						row.table_name,
						row.health_score,
						row.snapshot_count,
						row.file_count,
						row.delete_file_count,
						row.commits_last_hour,
						row.total_size_bytes,
						row.oldest_snapshot_age_hours,
						row.due_operations?.join(' ') ?? '',
						row.maintenance_mode ?? '',
						row.last_maintenance_at ?? '',
						row.last_optimize_at ?? '',
						row.last_optimize_manifests_at ?? '',
						row.last_expire_snapshots_at ?? '',
						row.last_remove_orphan_files_at ?? ''
					])
				)
			);
		} catch {
			toasts.error(m.monitoring_export_failed());
		}
	}

	async function exportQueries() {
		if (!engineId || exportingQueries) return;
		exportingQueries = true;
		try {
			// The visible table is one page; the export walks every page with the
			// current filters, so a stale table cannot truncate it.
			const rows: MonitoringQuery[] = [];
			let offset = 0;
			for (;;) {
				const page = await listQueries(engineId, {
					state: queryState || undefined,
					scope,
					q: queryText.trim() || undefined,
					limit: 100,
					offset
				});
				rows.push(...page.items);
				if (page.next_offset == null || page.items.length === 0) break;
				offset = page.next_offset;
			}
			downloadCsv(
				'monitoring-queries.csv',
				toCsv(
					[
						m.monitoring_query_id(),
						m.monitoring_state(),
						m.common_user(),
						m.monitoring_class(),
						m.common_catalog(),
						m.monitoring_schema(),
						m.monitoring_started(),
						m.monitoring_elapsed_ms(),
						m.monitoring_cpu_ms(),
						m.monitoring_peak_memory(),
						m.common_sql()
					],
					rows
						.sort(compareQueries)
						.map((query) => [
							query.trino_query_id,
							query.state,
							query.user,
							query.queue_class ?? '',
							query.catalog,
							query.schema ?? '',
							query.created_at,
							query.elapsed_ms,
							query.cpu_ms,
							query.peak_memory_bytes,
							query.sql_first_line
						])
				)
			);
		} catch {
			toasts.error(m.monitoring_export_failed());
		} finally {
			exportingQueries = false;
		}
	}

	function selectTab(next: 'cluster' | 'queries' | 'tables' | 'maintenance') {
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

	function openEditor(catalogId: string, namespace: string, table: string) {
		editor = { catalogId, namespace, table };
		editorOpen = true;
	}

	function openMaintenance(row: TableHealth) {
		maintainTarget = row;
		operations = {
			optimize: row.needs_compaction,
			expire_snapshots: row.needs_snapshot_expiry,
			remove_orphan_files: false
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

	// A table with maintenance parameters shows what they find due; one
	// without shows the default thresholds' needs (#500).
	function needsLabels(row: TableHealth): string[] {
		if (row.maintenance_mode) return row.due_operations.map(operationLabel);
		const out: string[] = [];
		if (row.needs_compaction) out.push(m.monitoring_compaction());
		if (row.needs_snapshot_expiry) out.push(m.monitoring_snapshot_expiry());
		return out;
	}

	function runFilter() {
		return {
			trigger: (runTrigger || undefined) as 'manual' | 'automatic' | 'dry_run' | undefined,
			operation: (runOperation || undefined) as
				'optimize' | 'optimize_manifests' | 'expire_snapshots' | 'remove_orphan_files' | undefined
		};
	}

	async function analyzeNow() {
		if (!catalogId) return;
		analyzing = true;
		try {
			await analyzeCatalogTableHealth(catalogId);
			toasts.success(m.maintenance_analysis_started());
			// The pass writes rows asynchronously; watch until they appear and
			// keep the button in-flight so it cannot start a duplicate run (#750).
			const controller = poll(loadHealth, {
				until: () => health.length > 0,
				intervals: [3000, 5000, 10000],
				maxDurationMs: 5 * 60_000
			});
			polls.push(controller);
			if ((await controller.done) === 'timeout') {
				toasts.error(m.maintenance_analysis_timeout());
			}
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			analyzing = false;
		}
	}

	// A table with parameters runs what they find due, through its own route.
	async function runDue(row: TableHealth) {
		try {
			await runTableMaintenance(
				{ catalogId: row.catalog_id, namespace: row.namespace, table: row.table_name },
				'next_due'
			);
			toasts.success(m.maintenance_run_started({ operation: m.maintenance_run_next_due() }));
			await loadHealth();
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		}
	}

	function modeLabel(mode: string): string {
		return mode === 'managed'
			? m.maintenance_mode_managed()
			: mode === 'off'
				? m.maintenance_mode_off()
				: m.maintenance_mode_elsewhere();
	}

	function scoreTone(score: number): 'success' | 'warning' | 'danger' {
		return score >= 80 ? 'success' : score >= 50 ? 'warning' : 'danger';
	}

	onMount(() => {
		// The awaits must not restart the live streams after the page is gone;
		// the abort signal is the cleanup the async body cannot return itself.
		const controller = new AbortController();
		void (async () => {
			await workspaceStore.load();
			if (controller.signal.aborted) return;
			await loadEngines();
			if (controller.signal.aborted) return;
			if (engines.length > 0) await loadActive();
		})();
		return () => controller.abort();
	});

	onDestroy(() => {
		stopPolls();
		stopEvents();
	});

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
		<div class="mb-4 flex flex-wrap items-center justify-between gap-2 border-b border-border">
			<div class="flex gap-1" role="tablist" aria-label={m.monitoring_monitoring_tabs()}>
				{#each [['cluster', m.monitoring_cluster()], ['queries', m.monitoring_queries()], ['tables', m.monitoring_iceberg_health()], ['maintenance', m.maintenance_title()]] as [value, label] (value)}
					<button
						type="button"
						role="tab"
						aria-selected={tab === value}
						class="-mb-px border-b-2 px-3 py-1.5 text-[12px] font-semibold {tab === value
							? 'border-primary text-primary'
							: 'border-transparent text-slate-600 hover:text-text-main dark:text-slate-300'}"
						onclick={() => selectTab(value as 'cluster' | 'queries' | 'tables' | 'maintenance')}
					>
						{label}
					</button>
				{/each}
			</div>
			{#if tab === 'cluster' || tab === 'queries'}
				<div class="flex items-center gap-2 pb-1">
					<Button size="sm" onclick={refreshActive}>{m.monitoring_refresh()}</Button>
					<Button size="sm" variant={live ? 'secondary' : 'primary'} onclick={() => setLive(!live)}>
						{live ? m.monitoring_pause_live() : m.monitoring_resume_live()}
					</Button>
				</div>
			{/if}
		</div>

		{#if tab === 'cluster'}
			<div class="mb-3 flex items-center gap-2">
				<div class="w-40">
					<Select
						label={m.monitoring_window()}
						bind:value={windowPreset}
						options={[
							{ value: '5m', label: m.monitoring_last_5_minutes() },
							{ value: '1h', label: m.monitoring_last_hour() },
							{ value: '6h', label: m.monitoring_last_6_hours() },
							{ value: '24h', label: m.monitoring_last_24_hours() },
							{ value: 'custom', label: m.monitoring_custom_window() }
						]}
						onchange={() => {
							void loadOverview();
							void loadCluster();
						}}
					/>
				</div>
				{#if windowPreset === 'custom'}
					<div class="w-44">
						<Input
							label={m.monitoring_window_duration()}
							bind:value={customWindow}
							placeholder="90m"
							onkeydown={(event) => {
								if (event.key !== 'Enter') return;
								void loadOverview();
								void loadCluster();
							}}
						/>
					</div>
				{/if}
				{#if cluster?.latest}
					<span class="text-[11px] text-slate-600 dark:text-slate-300">
						{m.monitoring_updated()}
						<RelativeTime at={cluster.latest.captured_at} live />
					</span>
				{/if}
			</div>
			{#if engines.length > 1}
				<section class="mb-4" aria-labelledby="engine-overview-heading">
					<div class="mb-2 flex items-center justify-between gap-2">
						<h2
							id="engine-overview-heading"
							class="text-[10px] font-semibold uppercase tracking-wide text-text-muted"
						>
							{m.monitoring_engines()}
						</h2>
						{#if isAdmin}
							<a
								class="text-[11px] font-semibold text-primary hover:underline"
								href={resolve('/settings/workload')}>{m.monitoring_open_workload()}</a
							>
						{/if}
					</div>
					{#if overviewLoading && overview.length === 0}
						<Skeleton class="h-24 w-full" />
					{:else}
						<div class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
							{#each overview as row (row.engine.id)}
								{@const health = engineHealth(row.latest)}
								{@const memory = row.latest ? memoryUsedPercent(row.latest) : null}
								<button
									type="button"
									aria-pressed={engineId === row.engine.id}
									onclick={() => selectEngine(row.engine.id)}
									class="rounded-lg border bg-bg-panel p-3 text-left transition-colors {engineId ===
									row.engine.id
										? 'border-primary'
										: 'border-border hover:border-border-hover'}"
								>
									<span class="flex items-center justify-between gap-2">
										<span class="truncate text-[12px] font-semibold">{row.engine.name}</span>
										<Badge tone={healthTone(health)}>{healthLabel(health)}</Badge>
									</span>
									<span class="mt-2 grid grid-cols-4 gap-2">
										{#each [[m.monitoring_workers(), row.latest?.active_workers], [m.monitoring_running(), row.latest?.running_queries], [m.monitoring_queued(), row.latest?.queued_queries], [m.monitoring_blocked(), row.latest?.blocked_queries]] as [label, value] (label)}
											<span class="block">
												<span class="block text-[10px] uppercase tracking-wide text-text-muted"
													>{label}</span
												>
												<span class="block text-[12px] font-semibold">{value ?? '—'}</span>
											</span>
										{/each}
									</span>
									<span class="mt-1 block text-[10px] text-text-muted">
										{m.monitoring_memory_used()}:
										<span class="font-semibold text-text-main"
											>{memory === null ? '—' : `${memory}%`}</span
										>
									</span>
									{#if row.history.length > 1}
										<span class="mt-1 block">
											<Sparkline points={row.history} />
										</span>
									{/if}
									{#if row.classes.length > 0}
										<span class="mt-1 flex flex-wrap gap-1">
											{#each row.classes as queue (queue.class)}
												<span
													class="rounded-full border border-border bg-bg-toolbar px-1.5 py-px text-[10px] text-text-muted"
												>
													{classLabel(queue.class)}: {m.monitoring_class_load({
														running: queue.running,
														queued: queue.queued
													})}
												</span>
											{/each}
										</span>
									{/if}
								</button>
							{/each}
						</div>
					{/if}
				</section>
			{/if}
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
					{#each [[m.monitoring_workers(), cluster.latest.active_workers], [m.monitoring_running(), cluster.latest.running_queries], [m.monitoring_queued(), cluster.latest.queued_queries], [m.monitoring_blocked(), cluster.latest.blocked_queries]] as [label, value] (label)}
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
					<TimeSeriesChart
						series={clusterSeries}
						title={m.monitoring_queries()}
						yLabel={m.monitoring_queries()}
					/>
				{/if}
			{/if}
		{:else if tab === 'queries'}
			<div
				class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-5"
			>
				<Select
					label={m.monitoring_scope()}
					bind:value={scope}
					options={[
						{
							value: 'mine',
							get label() {
								return m.monitoring_mine();
							}
						},
						...(isAdmin ? [{ value: 'all', label: m.monitoring_everyone() }] : [])
					]}
					onchange={() => loadQueries()}
				/>
				<Select
					label={m.monitoring_state()}
					bind:value={queryState}
					options={[
						{ value: '', label: m.monitoring_any() },
						{
							value: 'running',
							get label() {
								return m.monitoring_running();
							}
						},
						{
							value: 'finished',
							get label() {
								return m.monitoring_finished();
							}
						},
						{
							value: 'failed',
							get label() {
								return m.monitoring_failed();
							}
						},
						{
							value: 'cancelled',
							get label() {
								return m.monitoring_cancelled();
							}
						}
					]}
					onchange={() => loadQueries()}
				/>
				<Input label={m.monitoring_sql_contains()} bind:value={queryText} placeholder="orders" />
				<Select
					label={m.monitoring_sort()}
					bind:value={querySort}
					options={[
						{ value: 'created', label: m.monitoring_sort_created() },
						{ value: 'elapsed', label: m.monitoring_sort_elapsed() },
						{ value: 'cpu', label: m.monitoring_sort_cpu() },
						{ value: 'memory', label: m.monitoring_peak_memory() }
					]}
				/>
				<div class="flex items-end gap-2">
					<Button onclick={() => loadQueries()}>{m.monitoring_search()}</Button>
					<Button
						variant="secondary"
						loading={exportingQueries}
						disabled={exportingQueries}
						onclick={exportQueries}>{m.monitoring_export_csv()}</Button
					>
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
								<th class="px-3 py-2">{m.monitoring_class()}</th>
								<th class="px-3 py-2">SQL</th>
								<th class="px-3 py-2">{m.monitoring_catalog()}</th>
								<th class="px-3 py-2">{m.monitoring_elapsed()}</th>
								<th class="px-3 py-2">{m.monitoring_cpu()}</th>
								<th class="px-3 py-2">{m.monitoring_peak_memory()}</th>
								<th class="px-3 py-2">{m.monitoring_started()}</th>
								<th class="px-3 py-2 text-right">{m.monitoring_actions()}</th>
							</tr>
						</thead>
						<tbody>
							{#each sortedQueries as query (query.trino_query_id)}
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
									<td class="px-3 py-1.5">
										{#if query.queue_class}
											<Badge tone="neutral">{classLabel(query.queue_class)}</Badge>
										{:else}
											<span class="text-text-muted">—</span>
										{/if}
									</td>
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
									<td class="px-3 py-1.5">
										{query.peak_memory_bytes != null ? formatBytes(query.peak_memory_bytes) : '—'}
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
		{:else if tab === 'tables'}
			<div
				class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-5"
			>
				<Select
					label={m.monitoring_catalog()}
					bind:value={catalogId}
					options={catalogs.map((catalog) => ({ value: catalog.id, label: catalog.name }))}
					onchange={() => loadHealth()}
				/>
				<Input label={m.monitoring_schema()} bind:value={healthNamespace} placeholder="sales" />
				<Select
					label={m.monitoring_needs()}
					bind:value={needs}
					options={[
						{ value: '', label: m.monitoring_any() },
						{ value: 'compaction', label: m.monitoring_compaction() },
						{ value: 'snapshot_expiry', label: m.monitoring_snapshot_expiry_2() },
						{ value: 'optimize', label: m.maintenance_op_optimize() },
						{ value: 'optimize_manifests', label: m.maintenance_op_optimize_manifests() },
						{ value: 'expire_snapshots', label: m.maintenance_op_expire_snapshots() },
						{ value: 'remove_orphan_files', label: m.maintenance_op_remove_orphan_files() }
					]}
					onchange={() => loadHealth()}
				/>
				<Input label={m.monitoring_max_health_score()} bind:value={maxScore} placeholder="80" />
				<div class="flex items-end gap-2">
					<Button onclick={() => loadHealth()}>{m.monitoring_refresh()}</Button>
					<Button variant="secondary" onclick={exportHealth}>{m.monitoring_export_csv()}</Button>
				</div>
			</div>
			{#if selectedReadOnly}
				<p class="mb-3 flex items-center gap-2 text-[12px] text-text-muted" role="note">
					<Badge tone="neutral">{m.common_read_only()}</Badge>
					{m.maintenance_catalog_read_only()}
				</p>
			{/if}
			{#if catalogs.length === 0}
				<EmptyState
					title={m.monitoring_no_catalogs()}
					description={m.maintenance_no_iceberg_catalog()}
				>
					<a class="text-[12px] font-semibold text-primary" href={resolve('/connections')}
						>{m.maintenance_go_to_connections()}</a
					>
				</EmptyState>
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
					description={m.maintenance_no_health_yet()}
				>
					<Button size="sm" loading={analyzing} onclick={analyzeNow}
						>{m.maintenance_analyze_now()}</Button
					>
				</EmptyState>
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
								<th class="px-3 py-2">{m.maintenance_col_delete_files()}</th>
								<th class="px-3 py-2">{m.maintenance_col_commits_hour()}</th>
								<th class="px-3 py-2">{m.monitoring_size()}</th>
								<th class="px-3 py-2">{m.monitoring_oldest()}</th>
								<th class="px-3 py-2">{m.monitoring_needs()}</th>
								<th class="px-3 py-2">{m.maintenance_mode()}</th>
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
									<td class="px-3 py-1.5">{row.delete_file_count ?? '—'}</td>
									<td class="px-3 py-1.5">{row.commits_last_hour ?? '—'}</td>
									<td class="px-3 py-1.5">
										{row.total_size_bytes != null
											? m.monitoring_mib({
													value: Math.round(row.total_size_bytes / 1024 / 1024)
												})
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
										{#if row.maintenance_mode}
											<Badge tone="neutral">{modeLabel(row.maintenance_mode)}</Badge>
										{:else}
											<span class="text-text-muted">{m.maintenance_not_set()}</span>
										{/if}
									</td>
									<td class="px-3 py-1.5">
										{#if row.last_maintenance_at}
											<RelativeTime at={row.last_maintenance_at} />
										{:else}
											—
										{/if}
										<div class="mt-0.5 flex flex-col text-[10px] text-text-muted">
											{#each [[m.maintenance_op_optimize(), row.last_optimize_at], [m.maintenance_op_optimize_manifests(), row.last_optimize_manifests_at], [m.maintenance_op_expire_snapshots(), row.last_expire_snapshots_at], [m.maintenance_op_remove_orphan_files(), row.last_remove_orphan_files_at]] as [label, at] (label)}
												{#if at}
													<span>{label}: <RelativeTime {at} /></span>
												{/if}
											{/each}
										</div>
									</td>
									<td class="px-3 py-1.5 text-right">
										<div class="flex justify-end gap-1.5">
											<Button
												size="sm"
												onclick={() => openEditor(row.catalog_id, row.namespace, row.table_name)}
												>{m.maintenance_configure()}</Button
											>
											{#if row.maintenance_mode === 'managed' || row.maintenance_mode === 'off'}
												<Button
													size="sm"
													disabled={isReadOnly(row.catalog_id)}
													onclick={() => runDue(row)}>{m.maintenance_run_now()}</Button
												>
											{:else if !row.maintenance_mode}
												<Button
													size="sm"
													disabled={isReadOnly(row.catalog_id)}
													onclick={() => openMaintenance(row)}
													>{m.monitoring_run_maintenance_2()}</Button
												>
											{/if}
										</div>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
			{#if catalogs.length > 0 && !healthError && !(healthLoading && health.length === 0)}
				<div class="mt-5 flex flex-wrap items-end justify-between gap-2">
					<h2 class="text-[13px] font-semibold">{m.monitoring_maintenance_runs()}</h2>
					<div class="flex gap-2">
						<div class="w-40">
							<Select
								label={m.maintenance_filter_trigger()}
								bind:value={runTrigger}
								options={[
									{ value: '', label: m.monitoring_any() },
									{ value: 'automatic', label: m.maintenance_trigger_automatic() },
									{ value: 'manual', label: m.maintenance_trigger_manual() },
									{ value: 'dry_run', label: m.maintenance_trigger_dry_run() }
								]}
								onchange={() => loadHealth()}
							/>
						</div>
						<div class="w-44">
							<Select
								label={m.maintenance_filter_operation()}
								bind:value={runOperation}
								options={[
									{ value: '', label: m.monitoring_any() },
									{ value: 'optimize', label: m.maintenance_op_optimize() },
									{ value: 'optimize_manifests', label: m.maintenance_op_optimize_manifests() },
									{ value: 'expire_snapshots', label: m.maintenance_op_expire_snapshots() },
									{ value: 'remove_orphan_files', label: m.maintenance_op_remove_orphan_files() }
								]}
								onchange={() => loadHealth()}
							/>
						</div>
					</div>
				</div>
				<div class="mt-2 overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">{m.monitoring_status()}</th>
								<th class="px-3 py-2">{m.monitoring_table()}</th>
								<th class="px-3 py-2">{m.monitoring_operations()}</th>
								<th class="px-3 py-2">{m.maintenance_col_trigger()}</th>
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
									<td class="px-3 py-1.5">
										{run.operations.map(operationLabel).join(', ') || '—'}
										<!-- eslint-disable svelte/no-navigation-without-resolve -- external Temporal UI URL -->
										{#if run.workflow_url}
											<a
												class="ml-1 font-semibold text-primary hover:underline"
												href={run.workflow_url}
												target="_blank"
												rel="noopener noreferrer">{m.maintenance_open_temporal()}</a
											>
										{/if}
										<!-- eslint-enable svelte/no-navigation-without-resolve -->
									</td>
									<td class="px-3 py-1.5">{triggerLabel(run.trigger)}</td>
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
									<td class="px-3 py-3 text-slate-600 dark:text-slate-300" colspan="7">
										{m.monitoring_no_maintenance_runs_yet()}
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
				/>
			</div>
			{#if catalogs.length === 0}
				<EmptyState
					title={m.monitoring_no_catalogs()}
					description={m.maintenance_no_iceberg_catalog()}
				>
					<a class="text-[12px] font-semibold text-primary" href={resolve('/connections')}
						>{m.maintenance_go_to_connections()}</a
					>
				</EmptyState>
			{:else if catalogId}
				<CatalogMaintenancePlan
					{catalogId}
					readOnly={selectedReadOnly}
					onconfigure={(item) => openEditor(catalogId, item.namespace, item.table_name)}
				/>
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
								// The history route is resolve('/history') plus the execution query.
								// eslint-disable-next-line svelte/no-navigation-without-resolve
								void goto(`${resolve('/history')}?execution=${selectedQuery.execution_id}`);
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

<Drawer
	bind:open={editorOpen}
	title={editor
		? `${m.maintenance_title()} · ${editor.namespace}.${editor.table}`
		: m.maintenance_title()}
	size="lg"
	onclose={() => void loadHealth()}
>
	{#if editor}
		<TableMaintenance
			catalogId={editor.catalogId}
			namespace={editor.namespace}
			table={editor.table}
			readOnly={isReadOnly(editor.catalogId)}
		/>
	{/if}
</Drawer>
