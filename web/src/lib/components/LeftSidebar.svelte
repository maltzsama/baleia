<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import DataSourceTree from './DataSourceTree.svelte';
	import SavedQueriesList from './SavedQueriesList.svelte';
	import type { TableRef } from '$lib/metadata/client';

	let {
		collapsed = $bindable(false),
		activeTab = $bindable<'datasources' | 'queries'>('datasources'),
		onSelectTable
	}: {
		collapsed?: boolean;
		activeTab?: 'datasources' | 'queries';
		onSelectTable?: (t: TableRef | null) => void;
	} = $props();
	let search = $state('');
	let tree = $state<{ handleSearchKey: (event: KeyboardEvent) => void } | null>(null);

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
	class="bg-bg-panel border border-border rounded-[var(--radius)] shadow-sm flex flex-col overflow-hidden transition-[width] duration-200 shrink-0
	{collapsed ? 'w-9' : 'w-[260px]'}"
>
	{#if collapsed}
		<div class="flex flex-col items-center py-2.5 gap-2.5 flex-1">
			<button
				onclick={() => (collapsed = false)}
				title={m.common_expand()}
				class="w-[22px] h-[22px] rounded-[5px] cursor-pointer flex items-center justify-center text-text-muted transition-all duration-150 bg-none border-none
					hover:bg-blue-50 hover:text-primary dark:hover:bg-[#152236]"
			>
				<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
					><ellipse cx="12" cy="5" rx="9" ry="3" /><path
						d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"
					/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" /></svg
				>
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
						<svg viewBox="0 0 24 24" class="w-3 h-3"
							><ellipse cx="12" cy="5" rx="9" ry="3" /><path
								d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"
							/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" /></svg
						>
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
						<svg viewBox="0 0 24 24" class="w-3 h-3"
							><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><polyline
								points="14 2 14 8 20 8"
							/></svg
						>
						{m.components_leftsidebar_queries()}
					</button>
				</div>
				<button
					onclick={() => (collapsed = true)}
					title={m.components_leftsidebar_collapse()}
					aria-label={m.components_leftsidebar_collapse()}
					class="flex items-center px-2 border-l border-border text-text-muted cursor-pointer bg-transparent border-t-0 border-b-0 border-r-0 transition-colors duration-150 hover:text-text-main"
				>
					<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><path d="M15 18l-6-6 6-6" /></svg>
				</button>
			</div>

			<div class="p-2 border-b border-border shrink-0">
				<div
					class="w-full flex items-center gap-1.5 bg-slate-100 dark:bg-[#1c2128] border border-border rounded-md px-2 py-[5px] transition-all duration-150 focus-within:bg-bg-panel focus-within:border-primary"
				>
					<svg viewBox="0 0 24 24" class="w-3 h-3 text-text-muted shrink-0"
						><circle cx="11" cy="11" r="8" /><line x1="21" y1="21" x2="16.65" y2="16.65" /></svg
					>
					<input
						type="text"
						placeholder="Search tables and columns..."
						bind:value={search}
						onkeydown={(event) => tree?.handleSearchKey(event)}
						aria-label="Search tables and columns"
						class="border-none bg-transparent outline-none text-xs text-text-main w-full placeholder:text-text-muted"
					/>
				</div>
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
