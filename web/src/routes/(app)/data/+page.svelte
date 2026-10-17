<script lang="ts">
	import { onMount } from 'svelte';
	import DataSourceTree from '$lib/components/DataSourceTree.svelte';
	import TableDetail from '$lib/components/TableDetail.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import { selection } from '$lib/stores/selection.svelte';
	import { searchMetadata, type TableRef } from '$lib/metadata/client';
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	let selectedTable = $state<TableRef | null>(null);
	let search = $state('');
	let tree = $state<{ handleSearchKey: (event: KeyboardEvent) => void } | null>(null);

	onMount(async () => {
		if (selectedTable || !selection.table) return;
		await workspaceStore.load();
		const ws = workspaceStore.current?.id;
		if (!ws) return;
		try {
			const results = await searchMetadata(ws, selection.table, { kind: 'table', limit: 50 });
			const match = results.find(
				(result) =>
					result.kind === 'table' &&
					result.table === selection.table &&
					result.namespace.join('.') === selection.ns.join('.')
			);
			if (match) {
				selectedTable = {
					tableId: match.table_id,
					catalogId: match.catalog_id,
					catalogName: selection.catalog,
					namespace: match.namespace,
					tableName: match.table,
					bindings: []
				};
			}
		} catch {
			// The tree is still usable; a bad deep link just opens nothing.
		}
	});

	function select(table: TableRef | null) {
		selectedTable = table;
		selection.set({
			catalog: table?.catalogName ?? '',
			ns: table?.namespace ?? [],
			table: table?.tableName ?? ''
		});
	}
</script>

<div class="flex flex-1 gap-2.5 overflow-hidden p-2.5">
	<aside
		class="flex w-[320px] shrink-0 flex-col overflow-hidden rounded-[var(--radius)] border border-border bg-bg-panel shadow-sm"
	>
		<div class="border-b border-border px-3 py-2">
			<div
				class="flex items-center gap-1.5 rounded-md border border-border bg-bg-toolbar px-2 py-1 focus-within:border-primary"
			>
				<svg viewBox="0 0 24 24" class="h-3 w-3 shrink-0 text-text-muted"
					><circle cx="11" cy="11" r="8" /><line x1="21" y1="21" x2="16.65" y2="16.65" /></svg
				>
				<input
					type="text"
					placeholder="Search tables and columns..."
					aria-label="Search tables and columns"
					bind:value={search}
					onkeydown={(event) => tree?.handleSearchKey(event)}
					class="w-full border-none bg-transparent text-xs text-text-main outline-none placeholder:text-text-muted"
				/>
			</div>
		</div>
		<DataSourceTree bind:this={tree} onSelectTable={select} bind:search />
	</aside>

	<main
		class="flex min-w-0 flex-1 flex-col overflow-hidden rounded-[var(--radius)] border border-border bg-bg-panel shadow-sm"
	>
		{#if selectedTable}
			<TableDetail table={selectedTable} />
		{:else}
			<EmptyState
				title="Select a table"
				description="Browse the metadata-driven catalogs on the left to inspect a table and its columns."
			/>
		{/if}
	</main>
</div>
