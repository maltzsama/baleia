<script lang="ts">
	import DataSourceTree from './DataSourceTree.svelte';

	let { collapsed = $bindable(false), activeTab = $bindable<'datasources' | 'queries'>('datasources'), onSelectTable }: {
		collapsed?: boolean;
		activeTab?: 'datasources' | 'queries';
		onSelectTable?: (t: {catalog: string, namespace: string, table: string} | null) => void;
	} = $props();
	let search = $state('');
</script>

<aside class="bg-bg-panel border border-border rounded-[var(--radius)] shadow-sm flex flex-col overflow-hidden transition-[width] duration-200 shrink-0
	{collapsed ? 'w-9' : 'w-[260px]'}">

	{#if collapsed}
		<div class="flex flex-col items-center py-2.5 gap-2.5 flex-1">
			<button onclick={() => collapsed = false} title="Expand"
				class="w-[22px] h-[22px] rounded-[5px] cursor-pointer flex items-center justify-center text-text-muted transition-all duration-150 bg-none border-none
					hover:bg-blue-50 hover:text-primary dark:hover:bg-[#152236]">
				<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg>
			</button>
			<span class="[writing-mode:vertical-rl] text-[10px] font-semibold tracking-wide uppercase text-text-muted rotate-180 mt-1">Datasources</span>
		</div>
	{:else}
		<div class="flex flex-col flex-1 overflow-hidden">
			<div class="flex bg-slate-50 dark:bg-[#1c2128] border-b border-border shrink-0">
				<button onclick={() => activeTab = 'datasources'}
					class="flex-1 text-center py-[9px] px-1.5 text-[11px] font-semibold cursor-pointer flex items-center justify-center gap-1.5 border-t-0 border-l-0 border-r-0 border-b-2 transition-all duration-150 bg-transparent
						{activeTab === 'datasources' ? 'text-primary border-b-primary bg-bg-panel' : 'text-text-muted border-b-transparent hover:text-text-main'}">
					<svg viewBox="0 0 24 24" class="w-3 h-3"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg>
					Datasources
				</button>
				<button onclick={() => activeTab = 'queries'}
					class="flex-1 text-center py-[9px] px-1.5 text-[11px] font-semibold cursor-pointer flex items-center justify-center gap-1.5 border-t-0 border-l-0 border-r-0 border-b-2 transition-all duration-150 bg-transparent
						{activeTab === 'queries' ? 'text-primary border-b-primary bg-bg-panel' : 'text-text-muted border-b-transparent hover:text-text-main'}">
					<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
					Queries
				</button>
				<button onclick={() => collapsed = true} title="Collapse"
					class="flex items-center px-2 border-l border-border text-text-muted cursor-pointer bg-transparent border-t-0 border-b-0 border-r-0 transition-colors duration-150 hover:text-text-main">
					<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><path d="M15 18l-6-6 6-6"/></svg>
				</button>
			</div>

			<div class="p-2 border-b border-border shrink-0">
				<div class="w-full flex items-center gap-1.5 bg-slate-100 dark:bg-[#1c2128] border border-border rounded-md px-2 py-[5px] transition-all duration-150 focus-within:bg-bg-panel focus-within:border-primary">
					<svg viewBox="0 0 24 24" class="w-3 h-3 text-text-muted shrink-0"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
					<input type="text" placeholder="Search..." bind:value={search}
						class="border-none bg-transparent outline-none text-xs text-text-main w-full placeholder:text-text-muted">
				</div>
			</div>

			{#if activeTab === 'datasources'}
				<DataSourceTree {onSelectTable} />
			{:else}
				<div class="overflow-y-auto flex-1 px-1.5 pt-1.5 pb-2">
					<div class="text-[10px] font-bold tracking-wide uppercase text-text-muted pt-2 px-1.5 pb-1 flex items-center gap-1.5 after:content-[''] after:flex-1 after:h-px after:bg-border">Saved Queries</div>
					<p class="text-xs text-text-muted px-2 py-6 text-center">No saved queries yet.</p>
				</div>
			{/if}
		</div>
	{/if}
</aside>
