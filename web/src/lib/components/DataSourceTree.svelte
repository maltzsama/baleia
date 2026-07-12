<script lang="ts">
	import { onMount } from 'svelte';

	const token = localStorage.getItem('token') || '';
	const apiBase = '/api/v1';

	let { onSelectTable }: { onSelectTable?: (t: {catalog: string, namespace: string, table: string} | null) => void } = $props();

	let connections = $state<any[]>([]);
	let loading = $state(true);
	let error = $state('');

	let expandedCatalogs = $state<Set<string>>(new Set());
	let expandedNamespaces = $state<Set<string>>(new Set());
	let catalogData = $state<Record<string, any[]>>({});
	let namespaceData = $state<Record<string, string[]>>({});
	let tableData = $state<Record<string, string[]>>({});
	let tableLoading = $state<Record<string, boolean>>({});

	let selectedTable = $state<{catalog: string, namespace: string, table: string} | null>(null);

	onMount(() => { loadConnections(); });

	async function loadConnections() {
		loading = true; error = '';
		try {
			const res = await fetch(`${apiBase}/catalog-connections`, { headers: { Authorization: `Bearer ${token}` } });
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			connections = await res.json();
		} catch (e: any) { error = e.message; }
		loading = false;
	}

	async function toggleCatalogs(connId: string) {
		const key = connId;
		if (expandedCatalogs.has(key)) { expandedCatalogs.delete(key); }
		else {
			expandedCatalogs.add(key);
			if (!catalogData[key]) {
				try {
					const res = await fetch(`${apiBase}/connections/${connId}/catalogs`, { headers: { Authorization: `Bearer ${token}` } });
					catalogData[key] = res.ok ? await res.json() : [];
				} catch { catalogData[key] = []; }
			}
		}
		expandedCatalogs = new Set(expandedCatalogs);
	}

	async function toggleNamespaces(catalogId: string, ns: string) {
		const key = `${catalogId}:${ns}`;
		if (expandedNamespaces.has(key)) { expandedNamespaces.delete(key); }
		else {
			expandedNamespaces.add(key);
			if (!tableData[key]) {
				tableLoading[key] = true;
				try {
					const res = await fetch(`${apiBase}/catalogs/${catalogId}/namespaces/${ns}/tables`, { headers: { Authorization: `Bearer ${token}` } });
					if (res.ok) {
						const tables = await res.json();
						tableData[key] = tables.map((t: any) => t.name || '?');
					}
				} catch { tableData[key] = []; }
				tableLoading[key] = false;
			}
		}
		expandedNamespaces = new Set(expandedNamespaces);
	}

	async function toggleNamespaceList(catalogId: string) {
		const key = catalogId;
		if (expandedCatalogs.has(`ns-${key}`)) { expandedCatalogs.delete(`ns-${key}`); }
		else {
			expandedCatalogs.add(`ns-${key}`);
			if (!namespaceData[key]) {
				try {
					const res = await fetch(`${apiBase}/catalogs/${catalogId}/namespaces`, { headers: { Authorization: `Bearer ${token}` } });
					if (res.ok) {
						const nss = await res.json() as string[][];
						namespaceData[key] = nss.map(ns => ns.join('.'));
					}
				} catch { namespaceData[key] = []; }
			}
		}
		expandedCatalogs = new Set(expandedCatalogs);
	}

	function selectTable(catalogId: string, ns: string, table: string) {
		selectedTable = { catalog: catalogId, namespace: ns, table };
		onSelectTable?.(selectedTable);
	}

	function connColor(type: string) {
		switch (type) { case 'postgresql': return '#3b82f6'; case 'mysql': return '#f59e0b'; case 'polaris': case 'nessie': case 'iceberg': return '#7e22ce'; default: return '#94a3b8'; }
	}
</script>

<div class="overflow-y-auto flex-1 px-1.5 pb-2 text-[13px]">
	{#if loading}
		<p class="text-text-muted text-xs px-2 py-4">Loading...</p>
	{:else if error}
		<p class="text-red-500 text-xs px-2 py-4">{error}</p>
	{:else}
		{#each connections as conn (conn.id)}
			{@const connKey = conn.id}
			<button class="flex items-center w-full text-left py-[5px] px-1.5 rounded-[5px] cursor-pointer gap-1.5 hover:bg-slate-100 dark:hover:bg-[#1c2128] bg-none border-none text-inherit text-[13px]" onclick={() => toggleCatalogs(connKey)}>
				<svg viewBox="0 0 24 24" class="shrink-0 w-3.5 h-3.5 text-text-muted transition-transform duration-150 {expandedCatalogs.has(connKey) ? 'rotate-90' : ''}"><path d="M9 18l6-6-6-6"/></svg>
				<span class="shrink-0 w-3.5 h-3.5 rounded-sm flex items-center justify-center font-mono text-[9px] font-bold" style="background:{connColor(conn.type)}20;color:{connColor(conn.type)}">
					{conn.type.slice(0,2).toUpperCase()}
				</span>
				<span class="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap font-medium">{conn.name}</span>
			</button>
			{#if expandedCatalogs.has(connKey)}
				{#each (catalogData[connKey] || []) as cat (cat.id)}
					{@const catKey = cat.id}
					<button class="flex items-center w-full text-left py-[5px] px-1.5 pl-4 rounded-[5px] cursor-pointer gap-1.5 hover:bg-slate-100 dark:hover:bg-[#1c2128] bg-none border-none text-inherit text-[13px]" onclick={() => toggleNamespaceList(catKey)}>
						<svg viewBox="0 0 24 24" class="shrink-0 w-3.5 h-3.5 text-text-muted transition-transform duration-150 {expandedCatalogs.has(`ns-${catKey}`) ? 'rotate-90' : ''}"><path d="M9 18l6-6-6-6"/></svg>
						<svg viewBox="0 0 24 24" class="shrink-0 w-3.5 h-3.5 stroke-amber-500"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg>
						<span class="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{cat.name}</span>
					</button>
					{#if expandedCatalogs.has(`ns-${catKey}`)}
						{#each (namespaceData[catKey] || []) as ns (ns)}
							{@const nsKey = `${catKey}:${ns}`}
							<button class="flex items-center w-full text-left py-[5px] px-1.5 pl-8 rounded-[5px] cursor-pointer gap-1.5 hover:bg-slate-100 dark:hover:bg-[#1c2128] bg-none border-none text-inherit text-[13px]" onclick={() => toggleNamespaces(catKey, ns)}>
								<svg viewBox="0 0 24 24" class="shrink-0 w-3.5 h-3.5 text-text-muted transition-transform duration-150 {expandedNamespaces.has(nsKey) ? 'rotate-90' : ''}"><path d="M9 18l6-6-6-6"/></svg>
								<svg viewBox="0 0 24 24" class="shrink-0 w-3.5 h-3.5 stroke-slate-400"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
								<span class="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-text-muted">{ns}</span>
							</button>
							{#if expandedNamespaces.has(nsKey)}
								{#if tableLoading[nsKey]}
											<p class="text-[11px] text-text-muted pl-14 py-1">Loading...</p>
								{:else}
									{#each (tableData[nsKey] || []) as table (table)}
										<button class="flex items-center w-full text-left py-[5px] px-1.5 pl-14 rounded-[5px] cursor-pointer gap-1.5 hover:bg-slate-100 dark:hover:bg-[#1c2128] bg-none border-none text-inherit text-[13px]
											{selectedTable?.table === table && selectedTable?.namespace === ns ? 'bg-blue-50 dark:bg-[#152236] text-primary font-medium' : ''}"
											onclick={() => selectTable(catKey, ns, table)}>
											<svg viewBox="0 0 24 24" class="shrink-0 w-3.5 h-3.5 {selectedTable?.table === table && selectedTable?.namespace === ns ? 'stroke-primary' : 'stroke-text-muted'}"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>
											<span class="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{table}</span>
										</button>
									{/each}
								{/if}
							{/if}
						{/each}
					{/if}
				{/each}
			{/if}
		{/each}
	{/if}
</div>
