<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import DataSourceTree from '$lib/components/DataSourceTree.svelte';
	import TableDetail from '$lib/components/TableDetail.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import { selection } from '$lib/stores/selection.svelte';
	import { findTable, loadCatalogs, type TableRef } from '$lib/metadata/client';
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	let selectedTable = $state<TableRef | null>(null);
	let search = $state('');
	let tree = $state<DataSourceTree | null>(null);

	// A deep link (?catalog=<id>&ns=&table=) restores the table it names in
	// that catalog, revealed in the tree; it never falls back to another one.
	type LinkState = 'idle' | 'resolving' | 'missing' | 'error';
	let linkState = $state<LinkState>('idle');
	let linkError = $state('');

	async function restoreLink() {
		const catalogParam = selection.catalog;
		const ns = selection.ns;
		const table = selection.table;
		if (!catalogParam || !tree) return;
		linkState = 'resolving';
		linkError = '';
		try {
			await workspaceStore.load();
			const ws = workspaceStore.current?.id;
			if (!ws) {
				linkState = 'idle';
				return;
			}
			// Links written before #455 carry the catalog name.
			const catalogs = await loadCatalogs(ws);
			const catalog =
				catalogs.find((item) => item.id === catalogParam) ??
				catalogs.find((item) => item.name === catalogParam);
			if (!catalog) {
				linkState = table ? 'missing' : 'idle';
				return;
			}
			if (!table) {
				await tree.revealCatalog(catalog.id);
				linkState = 'idle';
				return;
			}
			const tableId = await findTable(ws, catalog.id, ns, table);
			if (!tableId) {
				linkState = 'missing';
				return;
			}
			await tree.revealTable(catalog.id, ns, { id: tableId, name: table });
			linkState = 'idle';
		} catch (e) {
			linkError = e instanceof Error ? e.message : String(e);
			linkState = 'error';
		}
	}

	onMount(() => {
		if (!selectedTable) void restoreLink();
	});

	function select(table: TableRef | null) {
		selectedTable = table;
		linkState = 'idle';
		selection.set({
			catalog: table?.catalogId ?? '',
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
					placeholder={m.data_search_tables_and_columns_2()}
					aria-label={m.data_search_tables_and_columns()}
					bind:value={search}
					onkeydown={(event) => tree?.handleSearchKey(event)}
					class="w-full border-none bg-transparent text-xs text-text-main outline-none placeholder:text-text-muted"
				/>
			</div>
		</div>
		<DataSourceTree bind:this={tree} onSelectTable={select} bind:search />
	</aside>

	<section
		class="flex min-w-0 flex-1 flex-col overflow-hidden rounded-[var(--radius)] border border-border bg-bg-panel shadow-sm"
	>
		{#if selectedTable}
			<TableDetail table={selectedTable} />
		{:else if linkState === 'resolving'}
			<div class="space-y-2 p-4" role="status" aria-label={m.data_opening_the_linked_table()}>
				<Skeleton class="h-5 w-1/3" />
				<Skeleton class="h-4 w-2/3" />
			</div>
		{:else if linkState === 'missing'}
			<EmptyState
				title={m.data_table_not_found()}
				description={m.data_table_not_found_description({
					table: [...selection.ns, selection.table].join('.')
				})}
			/>
		{:else if linkState === 'error'}
			<ErrorState
				title={m.data_could_not_open_the_linked_table()}
				message={linkError}
				onretry={restoreLink}
			/>
		{:else}
			<EmptyState
				title="Select a table"
				description="Browse the metadata-driven catalogs on the left to inspect a table and its columns."
			/>
		{/if}
	</section>
</div>
