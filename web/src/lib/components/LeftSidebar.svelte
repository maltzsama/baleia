<script lang="ts">
	import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import DatabasePlusIcon from '@lucide/svelte/icons/database-plus';
	import FileIcon from '@lucide/svelte/icons/file';
	import SearchIcon from '@lucide/svelte/icons/search';
	import { m } from '$lib/paraglide/messages';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import DataSourceTree from './DataSourceTree.svelte';
	import SavedQueriesList from './SavedQueriesList.svelte';
	import type { TableRef } from '$lib/metadata/client';

	let {
		collapsed = $bindable(false),
		activeTab = $bindable<'datasources' | 'queries'>('datasources'),
		onSelectTable,
		width = 260
	}: {
		collapsed?: boolean;
		activeTab?: 'datasources' | 'queries';
		onSelectTable?: (t: TableRef | null) => void;
		/** Expanded width in px (#468); the workbench makes it resizable. */
		width?: number;
	} = $props();
	let search = $state('');
	let tree = $state<DataSourceTree | null>(null);

	// Datasources/Queries are tabs (#466): arrows move between them and the
	// selected one is announced; the focus follows the selection.
	function onTabKey(event: KeyboardEvent) {
		if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
		event.preventDefault();
		activeTab = activeTab === 'datasources' ? 'queries' : 'datasources';
		document.getElementById(`left-tab-${activeTab}`)?.focus();
	}
</script>

<aside
	class="bg-bg-panel border border-border rounded-[var(--radius)] shadow-sm flex flex-col overflow-hidden shrink-0
	{collapsed ? 'w-9' : ''}"
	style:width={collapsed ? undefined : `${width}px`}
>
	{#if collapsed}
		<div class="flex flex-col items-center py-2.5 gap-2.5 flex-1">
			<button
				onclick={() => (collapsed = false)}
				title={m.common_expand()}
				class="w-[22px] h-[22px] rounded-[5px] cursor-pointer flex items-center justify-center text-text-muted transition-all duration-150 bg-none border-none
					hover:bg-blue-50 hover:text-primary dark:hover:bg-[#152236]"
			>
				<DatabaseIcon class="w-3.5 h-3.5" />
			</button>
			<span
				class="[writing-mode:vertical-rl] text-[10px] font-semibold tracking-wide uppercase text-text-muted rotate-180 mt-1"
				>{m.components_leftsidebar_datasources()}</span
			>
		</div>
	{:else}
		<div class="flex flex-col flex-1 overflow-hidden">
			<div class="flex bg-slate-50 dark:bg-[#1c2128] border-b border-border shrink-0">
				<div
					role="tablist"
					aria-label={m.components_leftsidebar_sidebar_views()}
					class="flex flex-1"
					onkeydown={onTabKey}
					tabindex="-1"
				>
					<button
						id="left-tab-datasources"
						role="tab"
						aria-selected={activeTab === 'datasources'}
						aria-controls="left-panel"
						tabindex={activeTab === 'datasources' ? 0 : -1}
						onclick={() => (activeTab = 'datasources')}
						class="flex-1 text-center py-[9px] px-1.5 text-[11px] font-semibold cursor-pointer flex items-center justify-center gap-1.5 border-t-0 border-l-0 border-r-0 border-b-2 transition-all duration-150 bg-transparent
						{activeTab === 'datasources'
							? 'text-primary border-b-primary bg-bg-panel'
							: 'text-text-muted border-b-transparent hover:text-text-main'}"
					>
						<DatabaseIcon class="w-3 h-3" />
						{m.components_leftsidebar_datasources()}
					</button>
					<button
						id="left-tab-queries"
						role="tab"
						aria-selected={activeTab === 'queries'}
						aria-controls="left-panel"
						tabindex={activeTab === 'queries' ? 0 : -1}
						onclick={() => (activeTab = 'queries')}
						class="flex-1 text-center py-[9px] px-1.5 text-[11px] font-semibold cursor-pointer flex items-center justify-center gap-1.5 border-t-0 border-l-0 border-r-0 border-b-2 transition-all duration-150 bg-transparent
						{activeTab === 'queries'
							? 'text-primary border-b-primary bg-bg-panel'
							: 'text-text-muted border-b-transparent hover:text-text-main'}"
					>
						<FileIcon class="w-3 h-3" />
						{m.components_leftsidebar_queries()}
					</button>
				</div>
				<button
					onclick={() => (collapsed = true)}
					title={m.components_leftsidebar_collapse()}
					aria-label={m.components_leftsidebar_collapse()}
					class="flex items-center px-2 border-l border-border text-text-muted cursor-pointer bg-transparent border-t-0 border-b-0 border-r-0 transition-colors duration-150 hover:text-text-main"
				>
					<ChevronLeftIcon class="w-3.5 h-3.5" />
				</button>
			</div>

			<div class="flex items-center gap-1.5 p-2 border-b border-border shrink-0">
				<div
					class="min-w-0 flex-1 flex items-center gap-1.5 bg-slate-100 dark:bg-[#1c2128] border border-border rounded-md px-2 py-[5px] transition-all duration-150 focus-within:bg-bg-panel focus-within:border-primary"
				>
					<SearchIcon class="w-3 h-3 text-text-muted shrink-0" />
					<input
						type="text"
						placeholder={m.components_leftsidebar_search_tables_and_columns()}
						bind:value={search}
						onkeydown={(event) => tree?.handleSearchKey(event)}
						{...tree?.comboboxAttrs()}
						aria-label={m.components_leftsidebar_search_tables_and_columns_2()}
						class="border-none bg-transparent outline-none text-xs text-text-main w-full placeholder:text-text-muted"
					/>
				</div>
				{#if activeTab === 'datasources' && workspaceStore.current?.allowed_actions?.includes('manage_grants')}
					<button
						type="button"
						class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-bg-toolbar hover:text-text-main focus-visible:outline-2 focus-visible:outline-primary"
						title={m.workspace_catalogs_title()}
						aria-label={m.workspace_catalogs_title()}
						onclick={() => tree?.openCatalogs()}
					>
						<DatabasePlusIcon class="h-4 w-4" aria-hidden="true" strokeWidth={1.7} />
					</button>
				{/if}
			</div>

			<div
				id="left-panel"
				role="tabpanel"
				aria-labelledby={activeTab === 'datasources' ? 'left-tab-datasources' : 'left-tab-queries'}
				class="flex min-h-0 flex-1 flex-col overflow-hidden"
			>
				{#if activeTab === 'datasources'}
					<DataSourceTree bind:this={tree} bind:search {onSelectTable} />
				{:else}
					<SavedQueriesList />
				{/if}
			</div>
		</div>
	{/if}
</aside>
