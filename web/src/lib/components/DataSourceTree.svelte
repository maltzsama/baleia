<script lang="ts">
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	type CatalogAccess = {
		connection_id: string;
		connection_name: string;
		connection_type: string;
		catalog_name: string;
	};

	const token = localStorage.getItem('token') || '';
	const apiBase = '/api/v1';

	let {
		onSelectTable
	}: {
		onSelectTable?: (
			t: { connectionId: string; catalog: string; namespace: string; table: string } | null
		) => void;
	} = $props();

	let catalogs = $state<CatalogAccess[]>([]);
	let loading = $state(true);
	let error = $state('');

	let expandedCatalogs = new SvelteSet<string>();
	let expandedNamespaces = new SvelteSet<string>();
	let namespaceData = $state<Record<string, string[]>>({});
	let tableData = $state<Record<string, string[]>>({});
	let tableLoading = $state<Record<string, boolean>>({});

	let selectedTable = $state<{
		connectionId: string;
		catalog: string;
		namespace: string;
		table: string;
	} | null>(null);

	function catalogKey(cat: CatalogAccess): string {
		return `${cat.connection_id}:${cat.catalog_name}`;
	}

	function namespaceKey(cat: CatalogAccess, ns: string): string {
		return `${catalogKey(cat)}:${ns}`;
	}

	function nsApiPath(cat: CatalogAccess): string {
		return `${apiBase}/connections/${cat.connection_id}/catalogs/${cat.catalog_name}/namespaces`;
	}

	function tablesApiPath(cat: CatalogAccess, ns: string): string {
		return `${nsApiPath(cat)}/${ns}/tables`;
	}

	onMount(() => {
		loadWorkspaceCatalogs();
	});

	$effect(() => {
		if (workspaceStore.current) loadWorkspaceCatalogs();
	});

	async function loadWorkspaceCatalogs() {
		if (!workspaceStore.current) return;
		loading = true;
		error = '';
		try {
			const res = await fetch(`${apiBase}/workspaces/${workspaceStore.current.id}/catalogs`, {
				headers: { Authorization: `Bearer ${token}` }
			});
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			catalogs = await res.json();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
		loading = false;
	}

	async function toggleNamespaces(cat: CatalogAccess, ns: string) {
		const key = namespaceKey(cat, ns);
		if (expandedNamespaces.has(key)) {
			expandedNamespaces.delete(key);
		} else {
			expandedNamespaces.add(key);
			if (!tableData[key]) {
				tableLoading[key] = true;
				try {
					const res = await fetch(tablesApiPath(cat, ns), {
						headers: { Authorization: `Bearer ${token}` }
					});
					if (res.ok) {
						const tables = await res.json();
						tableData[key] = (tables as { name?: string }[]).map((t) => t.name || '?');
					}
				} catch {
					tableData[key] = [];
				}
				tableLoading[key] = false;
			}
		}
	}

	async function toggleNamespaceList(cat: CatalogAccess) {
		const key = catalogKey(cat);
		if (expandedCatalogs.has(`ns-${key}`)) {
			expandedCatalogs.delete(`ns-${key}`);
		} else {
			expandedCatalogs.add(`ns-${key}`);
			if (!namespaceData[key]) {
				try {
					const res = await fetch(nsApiPath(cat), {
						headers: { Authorization: `Bearer ${token}` }
					});
					if (res.ok) {
						const nss = (await res.json()) as string[][];
						namespaceData[key] = nss.map((ns) => ns.join('.'));
					}
				} catch {
					namespaceData[key] = [];
				}
			}
		}
	}

	function selectTable(cat: CatalogAccess, ns: string, table: string) {
		selectedTable = {
			connectionId: cat.connection_id,
			catalog: cat.catalog_name,
			namespace: ns,
			table
		};
		onSelectTable?.(selectedTable);
	}

	function connColor(type: string) {
		switch (type) {
			case 'postgresql':
				return '#3b82f6';
			case 'mysql':
				return '#f59e0b';
			case 'polaris':
			case 'nessie':
			case 'iceberg':
				return '#7e22ce';
			default:
				return '#94a3b8';
		}
	}
</script>

<div class="overflow-y-auto flex-1 px-1.5 pb-2 text-[13px]">
	{#if !workspaceStore.current}
		<p class="text-text-muted text-xs px-2 py-4">Select a workspace to browse catalogs.</p>
	{:else if loading}
		<p class="text-text-muted text-xs px-2 py-4">Loading...</p>
	{:else if error}
		<p class="text-red-500 text-xs px-2 py-4">{error}</p>
	{:else if catalogs.length === 0}
		<p class="text-text-muted text-xs px-2 py-4">No catalogs in this workspace.</p>
	{:else}
		{#each catalogs as cat (catalogKey(cat))}
			{@const ckey = catalogKey(cat)}
			<button
				class="flex items-center w-full text-left py-[5px] px-1.5 rounded-[5px] cursor-pointer gap-1.5 hover:bg-slate-100 dark:hover:bg-[#1c2128] bg-none border-none text-inherit text-[13px]"
				onclick={() => toggleNamespaceList(cat)}
			>
				<svg
					viewBox="0 0 24 24"
					class="shrink-0 w-3.5 h-3.5 text-text-muted transition-transform duration-150 {expandedCatalogs.has(
						`ns-${ckey}`
					)
						? 'rotate-90'
						: ''}"><path d="M9 18l6-6-6-6" /></svg
				>
				<span
					class="shrink-0 w-3.5 h-3.5 rounded-sm flex items-center justify-center"
					style="color:{connColor(cat.connection_type)}"
				>
					{#if cat.connection_type === 'polaris' || cat.connection_type === 'iceberg'}
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
							><polygon
								points="12 2 15 9 22 9 16.5 14 18.5 21 12 16.5 5.5 21 7.5 14 2 9 9 9 12 2"
							/></svg
						>
					{:else if cat.connection_type === 'nessie'}
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
							><path d="M12 2a10 10 0 0 1 7 17.5" /><path d="M12 2a10 10 0 0 0-7 17.5" /><path
								d="M12 22v-8"
							/><path d="M8 8c0 2 2 4 4 4s4-2 4-4" /></svg
						>
					{:else if cat.connection_type === 'postgresql'}
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
							><path d="M12 3a3 3 0 0 0-3 3v3a3 3 0 0 0 6 0V6a3 3 0 0 0-3-3z" /><path
								d="M12 12v7"
							/><path d="M9 19h6" /></svg
						>
					{:else if cat.connection_type === 'mysql'}
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
							><ellipse cx="12" cy="5" rx="8" ry="3" /><path
								d="M4 5v5c0 1.66 3.58 3 8 3s8-1.34 8-3V5"
							/><path d="M4 13v5c0 1.66 3.58 3 8 3s8-1.34 8-3v-5" /></svg
						>
					{:else}
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
							><circle cx="12" cy="12" r="3" /><path d="M19 9a7 7 0 0 1 0 6" /><path
								d="M5 15a7 7 0 0 1 0-6"
							/><path d="M12 2v2" /><path d="M12 20v2" /></svg
						>
					{/if}
				</span>
				<span class="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap font-medium"
					>{cat.catalog_name}</span
				>
				<span class="text-[10px] text-text-muted">({cat.connection_name})</span>
			</button>
			{#if expandedCatalogs.has(`ns-${ckey}`)}
				{#each namespaceData[ckey] || [] as ns (ns)}
					{@const nkey = namespaceKey(cat, ns)}
					<button
						class="flex items-center w-full text-left py-[5px] px-1.5 pl-4 rounded-[5px] cursor-pointer gap-1.5 hover:bg-slate-100 dark:hover:bg-[#1c2128] bg-none border-none text-inherit text-[13px]"
						onclick={() => toggleNamespaces(cat, ns)}
					>
						<svg
							viewBox="0 0 24 24"
							class="shrink-0 w-3.5 h-3.5 text-text-muted transition-transform duration-150 {expandedNamespaces.has(
								nkey
							)
								? 'rotate-90'
								: ''}"><path d="M9 18l6-6-6-6" /></svg
						>
						<svg viewBox="0 0 24 24" class="shrink-0 w-3.5 h-3.5 stroke-slate-400"
							><path
								d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"
							/></svg
						>
						<span
							class="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-text-muted"
							>{ns}</span
						>
					</button>
					{#if expandedNamespaces.has(nkey)}
						{#if tableLoading[nkey]}
							<p class="text-[11px] text-text-muted pl-8 py-1">Loading...</p>
						{:else}
							{#each tableData[nkey] || [] as table (table)}
								<button
									class="flex items-center w-full text-left py-[5px] px-1.5 pl-8 rounded-[5px] cursor-pointer gap-1.5 hover:bg-slate-100 dark:hover:bg-[#1c2128] bg-none border-none text-inherit text-[13px]
									{selectedTable?.table === table && selectedTable?.namespace === ns
										? 'bg-blue-50 dark:bg-[#152236] text-primary font-medium'
										: ''}"
									onclick={() => selectTable(cat, ns, table)}
								>
									<svg
										viewBox="0 0 24 24"
										class="shrink-0 w-3.5 h-3.5 {selectedTable?.table === table &&
										selectedTable?.namespace === ns
											? 'stroke-primary'
											: 'stroke-text-muted'}"
										><rect x="3" y="3" width="18" height="18" rx="2" /><line
											x1="3"
											y1="9"
											x2="21"
											y2="9"
										/><line x1="9" y1="21" x2="9" y2="9" /></svg
									>
									<span class="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap"
										>{table}</span
									>
								</button>
							{/each}
						{/if}
					{/if}
				{/each}
			{/if}
		{/each}
	{/if}
</div>
