<script lang="ts">
	import { onMount } from 'svelte';

	const token = localStorage.getItem('token') || '';
	const apiBase = '/api/v1';

	let { collapsed = $bindable(false), selectedTable }: {
		collapsed?: boolean;
		selectedTable?: {catalog: string, namespace: string, table: string} | null;
	} = $props();

	let columns = $state<any[]>([]);
	let loading = $state(false);
	let colSearch = $state('');
	let expandedCol = $state<string | null>(null);

	$effect(() => {
		if (selectedTable) loadColumns();
		else columns = [];
	});

	async function loadColumns() {
		if (!selectedTable) return;
		loading = true;
		try {
			const res = await fetch(`${apiBase}/catalogs/${selectedTable.catalog}/namespaces/${selectedTable.namespace}/tables/${selectedTable.table}/columns`, {
				headers: { Authorization: `Bearer ${token}` }
			});
			if (res.ok) {
				columns = await res.json();
			}
		} catch {}
		loading = false;
	}

	const filteredColumns = $derived(
		columns.filter(c => {
			const name = (c.name || '').toLowerCase();
			if (colSearch && !name.includes(colSearch.toLowerCase())) return false;
			return true;
		})
	);

	function typeClass(type: string): string {
		const t = (type || '').toLowerCase();
		if (t.includes('char') || t.includes('text') || t.includes('uuid') || t.includes('json')) return 'type-string';
		if (t.includes('int') || t.includes('float') || t.includes('double') || t.includes('decimal') || t.includes('numeric')) return 'type-numeric';
		if (t.includes('time') || t.includes('date')) return 'type-temporal';
		if (t.includes('bool')) return 'type-bool';
		return 'type-other';
	}
</script>

<aside class="bg-bg-panel border border-border rounded-[var(--radius)] shadow-sm flex flex-col overflow-hidden transition-[width] duration-200 shrink-0
	{collapsed ? 'w-9' : 'w-[292px]'}">

	{#if collapsed}
		<div class="flex flex-col items-center py-2.5 gap-2.5 flex-1">
			<button onclick={() => collapsed = false} title="Expand"
				class="w-[22px] h-[22px] rounded-[5px] cursor-pointer flex items-center justify-center text-text-muted bg-none border-none
					hover:bg-blue-50 hover:text-primary dark:hover:bg-[#152236]">
				<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
			</button>
			<span class="[writing-mode:vertical-rl] text-[10px] font-semibold tracking-wide uppercase text-text-muted rotate-180 mt-1">Inspector</span>
		</div>
	{:else}
		<div class="flex flex-col flex-1 overflow-hidden">
			{#if loading}
				<div class="p-4 text-xs text-text-muted">Loading...</div>
			{:else if !selectedTable}
				<div class="flex-1 flex items-center justify-center">
					<p class="text-xs text-text-muted px-4 text-center">Select a table to inspect its columns.</p>
				</div>
			{:else}
				<div class="flex flex-col flex-1 overflow-hidden">
					<div class="shrink-0 border-b border-border">
						<div class="flex items-center gap-2 px-2.5 py-[7px] border-b border-border bg-bg-toolbar">
							<span class="flex items-center gap-1 bg-red-50 dark:bg-[#1f0f0f] border border-red-200 dark:border-[#4d1111] text-red-600 dark:text-red-400 text-[9px] font-bold tracking-[0.5px] uppercase px-1.5 py-0.5 rounded-[3px] font-mono shrink-0">
								<span class="w-[5px] h-[5px] rounded-full bg-red-500 shrink-0"></span>TABLE
							</span>
							<span class="flex-1 font-mono text-[11px] text-text-muted overflow-hidden text-ellipsis whitespace-nowrap min-w-0">
								{selectedTable.namespace}
							</span>
							<button onclick={() => collapsed = true} title="Collapse"
								class="w-5 h-5 rounded flex items-center justify-center text-text-muted bg-none border-none cursor-pointer hover:bg-border hover:text-text-main">
								<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M9 18l6-6-6-6"/></svg>
							</button>
						</div>

						<div class="p-3 bg-bg-panel">
							<div class="flex items-center gap-[7px]">
								<svg viewBox="0 0 24 24" class="w-4 h-4 shrink-0 stroke-primary"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>
								<div>
									<span class="text-[15px] font-bold font-mono leading-none">{selectedTable.table}</span>
									<div class="text-[11px] text-text-muted mt-0.5">{columns.length} columns</div>
								</div>
							</div>
						</div>
					</div>

					<div class="px-2.5 py-[7px] border-b border-border bg-bg-panel shrink-0">
						<div class="flex items-center gap-[5px] bg-slate-100 dark:bg-[#1c2128] border border-border rounded px-[7px] py-1 transition-all duration-150 focus-within:bg-bg-panel focus-within:border-primary">
							<svg viewBox="0 0 24 24" class="w-3 h-3 text-text-muted shrink-0"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
							<input type="text" placeholder="Search columns..." bind:value={colSearch}
								class="border-none bg-transparent outline-none text-xs text-text-main w-full placeholder:text-text-muted">
						</div>
					</div>

					<div class="flex-1 overflow-y-auto">
						{#each filteredColumns as col (col.name)}
							<button class="flex flex-col w-full text-left py-2 px-3 border-b border-border gap-2 hover:bg-slate-50 dark:hover:bg-[#1c2128] cursor-pointer bg-none border-t-0 border-l-0 border-r-0 text-inherit"
								onclick={() => expandedCol = expandedCol === col.name ? null : col.name}>
								<div class="flex items-center gap-[5px]">
									<span class="text-xs font-semibold font-mono overflow-hidden text-ellipsis whitespace-nowrap flex-1 min-w-0">{col.name}</span>
									<span class="type-chip inline-flex items-center text-[10px] font-semibold font-mono py-px px-[5px] rounded-[3px] border border-transparent whitespace-nowrap shrink-0 {typeClass(col.type || '')}">{col.type || '?'}</span>
									{#if col.doc}
										<svg viewBox="0 0 24 24" class="w-3 h-3 text-text-muted shrink-0 transition-transform duration-150 {expandedCol === col.name ? 'rotate-180' : ''}"><path d="M6 9l6 6 6-6"/></svg>
									{/if}
								</div>
								{#if expandedCol === col.name && col.doc}
									<div class="text-[11px] text-text-muted leading-[1.45] pl-0">{col.doc}</div>
								{/if}
							</button>
						{/each}
					</div>
				</div>
			{/if}
		</div>
	{/if}
</aside>

<style>
	.type-string  { color: #0f766e; background: #f0fdf9; border-color: #99f6e4; }
	.type-numeric { color: #7c3aed; background: #f5f3ff; border-color: #ddd6fe; }
	.type-temporal{ color: #b45309; background: #fffbeb; border-color: #fde68a; }
	.type-bool    { color: #dc2626; background: #fef2f2; border-color: #fecaca; }
	.type-other   { color: #475569; background: #f8fafc; border-color: #e2e8f0; }
	.dark .type-other { color: #94a3b8; background: #1c2128; border-color: #30363d; }
</style>
