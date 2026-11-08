<script lang="ts">
	// Per-table Iceberg maintenance (#500) in its own page. Monitoring links
	// here with the same ?catalog=&ns=&table= selection the data explorer uses,
	// so the page is linkable, refreshable and shareable.
	import { m } from '$lib/paraglide/messages';
	import { resolve } from '$app/paths';
	import { selection } from '$lib/stores/selection.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { findTable, loadTable, type MetadataTableDetail } from '$lib/metadata/client';
	import TableMaintenance from '$lib/maintenance/TableMaintenance.svelte';
	import type { MaintenanceRun } from '$lib/maintenance/client';
	import { operationLabel, triggerLabel } from '$lib/maintenance/labels';
	import ActivityPanel from '$lib/audit/ActivityPanel.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const catalogId = $derived(selection.catalog);
	const namespace = $derived(selection.ns);
	const tableName = $derived(selection.table);

	// The component reports the table's runs; the latest one is the page's
	// top-right summary, so it shows without scrolling past the parameters.
	let runs = $state<MaintenanceRun[]>([]);
	const lastRun = $derived(runs[0] ?? null);
	$effect(() => {
		// A new selection starts from no runs.
		void catalogId;
		void namespace;
		void tableName;
		runs = [];
	});

	type MaintenanceResult =
		| { kind: 'empty' }
		| { kind: 'missing' }
		| { kind: 'error'; message: string }
		| { kind: 'found'; detail: MetadataTableDetail };

	let retryTick = $state(0);

	// The read is derived from the URL selection (and the retry tick): a new
	// selection produces a new promise, and the await block drops the previous
	// one. The workspace loads asynchronously, so its id is a dependency too.
	const result = $derived.by((): Promise<MaintenanceResult> => {
		const ws = workspaceId;
		const catalog = catalogId;
		const ns = namespace;
		const table = tableName;
		void retryTick;
		if (!ws || !catalog || !table) return Promise.resolve({ kind: 'empty' });
		return load(ws, catalog, ns, table);
	});

	async function load(
		ws: string,
		catalog: string,
		ns: string[],
		table: string
	): Promise<MaintenanceResult> {
		try {
			const tableId = await findTable(ws, catalog, ns, table);
			if (!tableId) return { kind: 'missing' };
			return { kind: 'found', detail: await loadTable(ws, tableId) };
		} catch (e) {
			return { kind: 'error', message: e instanceof Error ? e.message : String(e) };
		}
	}

	function retry() {
		retryTick += 1;
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-start justify-between gap-4">
		<div class="min-w-0">
			<a
				class="text-[12px] font-semibold text-primary hover:underline"
				href={resolve('/monitoring')}>{m.maintenance_back_to_monitoring()}</a
			>
			<h1 class="mt-1 text-xl font-semibold tracking-[-0.3px]">{m.maintenance_title()}</h1>
			{#await result then state}
				{#if state.kind === 'found'}
					<p class="mt-1 font-mono text-[13px] text-slate-600 dark:text-slate-300">
						{state.detail.qualified_name ?? [...namespace, tableName].join('.')}
					</p>
				{/if}
			{/await}
		</div>
		{#if tableName}
			<div class="shrink-0 text-right" data-testid="last-run">
				<p class="text-[10px] font-bold uppercase tracking-wide text-text-muted">
					{m.maintenance_col_last_run()}
				</p>
				{#if lastRun}
					<div class="mt-1 flex flex-wrap items-center justify-end gap-x-2 gap-y-1 text-[12px]">
						<StatusBadge kind="maintenance" status={lastRun.status} />
						<span>{lastRun.operations.map(operationLabel).join(', ') || '—'}</span>
						<Badge tone="neutral">{triggerLabel(lastRun.trigger)}</Badge>
						<RelativeTime at={lastRun.started_at} />
						{#if lastRun.error_code}
							<span class="font-mono text-danger">{lastRun.error_code}</span>
						{/if}
					</div>
				{:else}
					<p class="mt-1 text-[12px] text-text-muted">{m.maintenance_no_runs()}</p>
				{/if}
			</div>
		{/if}
	</div>

	{#await result}
		<div class="flex flex-col gap-2" role="status" aria-label={m.maintenance_loading()}>
			<Skeleton class="h-16 w-full" />
			<Skeleton class="h-40 w-full" />
		</div>
	{:then state}
		{#if state.kind === 'empty'}
			<EmptyState
				title={m.maintenance_select_a_table()}
				description={m.maintenance_select_a_table_hint()}
			>
				<a
					class="text-[12px] font-semibold text-primary hover:underline"
					href={resolve('/monitoring')}>{m.common_monitoring()}</a
				>
			</EmptyState>
		{:else if state.kind === 'missing'}
			<EmptyState
				title={m.data_table_not_found()}
				description={m.data_table_not_found_description({
					table: [...namespace, tableName].join('.')
				})}
			/>
		{:else if state.kind === 'error'}
			<ErrorState title={m.maintenance_could_not_load()} message={state.message} onretry={retry} />
		{:else}
			<TableMaintenance
				catalogId={state.detail.catalog_id}
				namespace={state.detail.namespace.join('.')}
				table={state.detail.name}
				onruns={(next) => (runs = next)}
			/>
			<div class="mt-4">
				<ActivityPanel
					resourceType="table_maintenance"
					resourceId={`${state.detail.catalog_id}/${state.detail.namespace.join('.')}/${state.detail.name}`}
					title={m.maintenance_activity()}
				/>
			</div>
		{/if}
	{:catch e}
		<ErrorState
			title={m.maintenance_could_not_load()}
			message={e instanceof Error ? e.message : String(e)}
			onretry={retry}
		/>
	{/await}
</div>
