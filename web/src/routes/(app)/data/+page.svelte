<script lang="ts">
	import DataSourceTree from '$lib/components/DataSourceTree.svelte';
	import RightSidebar from '$lib/components/RightSidebar.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import { selection } from '$lib/stores/selection.svelte';

	type SelectedTable = {
		connectionId: string;
		catalog: string;
		namespace: string;
		table: string;
	};

	let selectedTable = $state<SelectedTable | null>(null);
	let rightCollapsed = $state(false);

	function select(table: SelectedTable | null) {
		selectedTable = table;
		selection.set({
			catalog: table?.catalog ?? '',
			ns: table ? [table.namespace] : [],
			table: table?.table ?? ''
		});
	}
</script>

<div class="flex flex-1 gap-2.5 overflow-hidden p-2.5">
	<aside
		class="flex w-[300px] shrink-0 flex-col overflow-hidden rounded-[var(--radius)] border border-border bg-bg-panel shadow-sm"
	>
		<div
			class="border-b border-border px-3 py-2 text-[11px] font-bold uppercase tracking-wide text-text-muted"
		>
			Catalogs
		</div>
		<DataSourceTree onSelectTable={select} />
	</aside>

	<main
		class="flex min-w-0 flex-1 flex-col overflow-hidden rounded-[var(--radius)] border border-border bg-bg-panel shadow-sm"
	>
		{#if selectedTable}
			<div class="border-b border-border px-4 py-3">
				<div class="font-mono text-sm font-semibold">
					{selectedTable.catalog}.{selectedTable.namespace}.{selectedTable.table}
				</div>
				<p class="mt-1 text-[12px] text-text-muted">
					The metadata-driven table panel lands with #23.
				</p>
			</div>
			<div class="flex-1"></div>
		{:else}
			<EmptyState
				title="Select a table"
				description="Browse the catalogs on the left to inspect a table and its columns."
			/>
		{/if}
	</main>

	<RightSidebar bind:collapsed={rightCollapsed} {selectedTable} />
</div>
