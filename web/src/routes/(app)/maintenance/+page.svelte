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
	import ActivityPanel from '$lib/audit/ActivityPanel.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const catalogId = $derived(selection.catalog);
	const namespace = $derived(selection.ns);
	const tableName = $derived(selection.table);

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
	<div class="mb-4">
		<a class="text-[12px] font-semibold text-primary hover:underline" href={resolve('/monitoring')}
			>{m.maintenance_back_to_monitoring()}</a
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
