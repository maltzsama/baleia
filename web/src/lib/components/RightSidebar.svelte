<script lang="ts">
	let { collapsed = $bindable(false) } = $props();
	let inspectorTab = $state<'columns' | 'details'>('columns');
	let colSearch = $state('');
	let colFilter = $state<'all' | 'pk' | 'partition' | 'nullable'>('all');
</script>

<aside class="bg-bg-panel border border-border rounded-[var(--radius)] shadow-sm flex flex-col overflow-hidden transition-[width] duration-200 shrink-0
	{collapsed ? 'w-9' : 'w-[292px]'}">

	{#if collapsed}
		<div class="flex flex-col items-center py-2.5 gap-2.5 flex-1">
			<button
				onclick={() => collapsed = false}
				title="Expand"
				class="w-[22px] h-[22px] rounded-[5px] cursor-pointer flex items-center justify-center text-text-muted transition-all duration-150 bg-none border-none
					hover:bg-blue-50 hover:text-primary dark:hover:bg-[#152236]">
				<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
			</button>
			<span class="[writing-mode:vertical-rl] text-[10px] font-semibold tracking-wide uppercase text-text-muted rotate-180 mt-1">
				Inspector
			</span>
		</div>
	{:else}
		<div class="flex flex-col flex-1 overflow-hidden">

			<div class="shrink-0 border-b border-border">
				<div class="flex items-center gap-2 py-[7px] px-2.5 border-b border-border bg-bg-toolbar">
					<span class="flex items-center gap-1 bg-red-50 dark:bg-[#1f0f0f] border border-red-200 dark:border-[#4d1111] text-red-600 dark:text-red-400 text-[9px] font-bold tracking-wide py-0.5 px-1.5 rounded-[3px] font-mono uppercase shrink-0">
						<span class="w-[5px] h-[5px] rounded-full bg-red-500"></span>ICEBERG
					</span>
					<span class="flex-1 font-mono text-[11px] text-text-muted overflow-hidden text-ellipsis whitespace-nowrap">
						default / <b class="text-text-main font-semibold">users</b>
					</span>
					<button
						onclick={() => collapsed = true}
						title="Collapse"
						class="w-5 h-5 rounded shrink-0 cursor-pointer flex items-center justify-center text-text-muted transition-all duration-150 bg-none border-none hover:bg-slate-200 hover:text-text-main">
						<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M9 18l15-6-6-6"/></svg>
					</button>
				</div>

				<div class="py-3 px-3 pb-2.5 bg-bg-panel">
					<div class="flex items-center gap-[7px] mb-2">
						<svg viewBox="0 0 24 24" class="w-4 h-4 shrink-0 text-primary"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>
						<span class="text-[15px] font-bold font-mono">users</span>
					</div>
					<div class="text-xs text-text-muted leading-[1.55] bg-slate-50 dark:bg-[#1c2128] border border-border rounded-[4px] py-[7px] px-[9px] mb-2.5 max-h-20 overflow-y-auto break-words">
						User accounts with profile, preferences and activity tracking.
					</div>
					<div class="flex border border-border rounded-[5px] overflow-hidden mt-0.5">
						<div class="flex-1 py-[5px] text-center border-r border-border">
							<span class="block text-xs font-bold font-mono leading-tight">24</span>
							<span class="block text-[9px] font-semibold uppercase tracking-[0.4px] text-text-muted mt-px">Columns</span>
						</div>
						<div class="flex-1 py-[5px] text-center border-r border-border">
							<span class="block text-xs font-bold font-mono leading-tight">1.2M</span>
							<span class="block text-[9px] font-semibold uppercase tracking-[0.4px] text-text-muted mt-px">Rows</span>
						</div>
						<div class="flex-1 py-[5px] text-center">
							<span class="block text-xs font-bold font-mono leading-tight">48 MB</span>
							<span class="block text-[9px] font-semibold uppercase tracking-[0.4px] text-text-muted mt-px">Size</span>
						</div>
					</div>
				</div>
			</div>

			<div class="flex border-b border-border bg-bg-toolbar shrink-0">
				<button
					onclick={() => inspectorTab = 'columns'}
					style="flex:1.2"
					class="text-center py-[7px] px-1 text-[11px] font-semibold cursor-pointer border-t-0 border-l-0 border-r-0 border-b-2 transition-all duration-150 bg-transparent
						{inspectorTab === 'columns' ? 'text-primary border-b-primary bg-bg-panel' : 'text-text-muted border-b-transparent hover:text-text-main'}">
					Columns
				</button>
				<button
					onclick={() => inspectorTab = 'details'}
					class="flex-1 text-center py-[7px] px-1 text-[11px] font-semibold cursor-pointer border-t-0 border-l-0 border-r-0 border-b-2 transition-all duration-150 bg-transparent
						{inspectorTab === 'details' ? 'text-primary border-b-primary bg-bg-panel' : 'text-text-muted border-b-transparent hover:text-text-main'}">
					Details
				</button>
			</div>

			<div class="flex items-center py-[7px] px-2.5 border-b border-border bg-bg-panel shrink-0">
				<div class="flex-1 flex items-center gap-[5px] bg-slate-100 dark:bg-[#1c2128] border border-border rounded-[4px] py-1 px-[7px] transition-all duration-150 focus-within:bg-bg-panel focus-within:border-primary">
					<svg viewBox="0 0 24 24" class="w-3 h-3 text-text-muted shrink-0"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
					<input
						type="text"
						placeholder="Search columns..."
						bind:value={colSearch}
						class="border-none bg-transparent outline-none text-xs text-text-main w-full">
				</div>
			</div>

			<div class="flex gap-1 py-2 px-2.5 bg-bg-panel shrink-0">
				<button
					onclick={() => colFilter = 'all'}
					class="text-[10px] font-semibold py-0.5 px-[7px] rounded-[3px] cursor-pointer border transition-all duration-150
						{colFilter === 'all' ? 'bg-blue-50 dark:bg-[#152236] border-blue-200 text-primary' : 'border-border bg-transparent text-text-muted hover:border-primary hover:text-primary'}">
					All
				</button>
				<button
					onclick={() => colFilter = 'pk'}
					class="text-[10px] font-semibold py-0.5 px-[7px] rounded-[3px] cursor-pointer border transition-all duration-150
						{colFilter === 'pk' ? 'bg-blue-50 dark:bg-[#152236] border-blue-200 text-primary' : 'border-border bg-transparent text-text-muted hover:border-primary hover:text-primary'}">
					PK
				</button>
				<button
					onclick={() => colFilter = 'partition'}
					class="text-[10px] font-semibold py-0.5 px-[7px] rounded-[3px] cursor-pointer border transition-all duration-150
						{colFilter === 'partition' ? 'bg-blue-50 dark:bg-[#152236] border-blue-200 text-primary' : 'border-border bg-transparent text-text-muted hover:border-primary hover:text-primary'}">
					Partition
				</button>
				<button
					onclick={() => colFilter = 'nullable'}
					class="text-[10px] font-semibold py-0.5 px-[7px] rounded-[3px] cursor-pointer border transition-all duration-150
						{colFilter === 'nullable' ? 'bg-blue-50 dark:bg-[#152236] border-blue-200 text-primary' : 'border-border bg-transparent text-text-muted hover:border-primary hover:text-primary'}">
					Nullable
				</button>
			</div>

			<div class="flex-1 overflow-y-auto">
				<div class="flex items-start py-2 px-3 border-b border-border gap-2 hover:bg-slate-50 dark:hover:bg-[#1c2128]">
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-[5px] mb-[3px]">
							<span class="text-amber-500 shrink-0">
								<svg viewBox="0 0 24 24" class="w-[11px] h-[11px]"><polygon points="12 2 22 8.5 22 15.5 12 22 2 15.5 2 8.5 12 2"/><line x1="12" y1="22" x2="12" y2="15.5"/><polyline points="22 8.5 12 15.5 2 8.5"/></svg>
							</span>
							<span class="text-xs font-semibold font-mono flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">id</span>
							<span class="inline-flex items-center text-[10px] font-semibold font-mono py-px px-[5px] rounded-[3px] border whitespace-nowrap shrink-0"
								style="color: var(--color-type-numeric); background: var(--color-type-numeric-bg); border-color: var(--color-type-numeric-border);">
								UUID
							</span>
						</div>
					</div>
				</div>
				<div class="flex items-start py-2 px-3 border-b border-border gap-2 hover:bg-slate-50 dark:hover:bg-[#1c2128]">
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-[5px] mb-[3px]">
							<span class="text-xs font-semibold font-mono flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">email</span>
							<span class="inline-flex items-center text-[10px] font-semibold font-mono py-px px-[5px] rounded-[3px] border whitespace-nowrap shrink-0"
								style="color: var(--color-type-string); background: var(--color-type-string-bg); border-color: var(--color-type-string-border);">
								varchar
							</span>
						</div>
					</div>
				</div>
				<div class="flex items-start py-2 px-2.5 border-b border-border gap-2 border-l-2 border-l-amber-500 bg-amber-50/40 dark:bg-[#1f1a00] dark:border-l-[#b45309] hover:bg-slate-50 dark:hover:bg-[#1c2128]">
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-[5px] mb-[3px]">
							<span class="text-[9px] font-bold font-mono py-px px-1 rounded-[2px] bg-amber-100 border border-amber-200 text-amber-700 shrink-0">P</span>
							<span class="text-xs font-semibold font-mono flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">created_at</span>
							<span class="inline-flex items-center text-[10px] font-semibold font-mono py-px px-[5px] rounded-[3px] border whitespace-nowrap shrink-0"
								style="color: var(--color-type-temporal); background: var(--color-type-temporal-bg); border-color: var(--color-type-temporal-border);">
								timestamp
							</span>
						</div>
					</div>
				</div>
				<div class="flex items-start py-2 px-3 border-b border-border gap-2 hover:bg-slate-50 dark:hover:bg-[#1c2128]">
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-[5px] mb-[3px]">
							<span class="text-xs font-semibold font-mono flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">name</span>
							<span class="inline-flex items-center text-[10px] font-semibold font-mono py-px px-[5px] rounded-[3px] border whitespace-nowrap shrink-0"
								style="color: var(--color-type-string); background: var(--color-type-string-bg); border-color: var(--color-type-string-border);">
								varchar
							</span>
						</div>
						<div class="text-[11px] text-text-muted leading-[1.45]">User display name</div>
					</div>
				</div>
				<div class="flex items-start py-2 px-3 border-b border-border gap-2 hover:bg-slate-50 dark:hover:bg-[#1c2128]">
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-[5px] mb-[3px]">
							<span class="text-xs font-semibold font-mono flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">is_active</span>
							<span class="inline-flex items-center text-[10px] font-semibold font-mono py-px px-[5px] rounded-[3px] border whitespace-nowrap shrink-0"
								style="color: var(--color-type-bool); background: var(--color-type-bool-bg); border-color: var(--color-type-bool-border);">
								boolean
							</span>
						</div>
					</div>
				</div>
			</div>
		</div>
	{/if}
</aside>