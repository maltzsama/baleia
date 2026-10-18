<script lang="ts">
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import {
		loadCatalogs,
		loadNamespaces,
		loadTables,
		searchMetadata,
		type MetadataCatalog,
		type MetadataFreshness,
		type MetadataSearchResult,
		type MetadataTablePage,
		type MetadataTableSummary,
		type TableRef
	} from '$lib/metadata/client';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import EngineBadge from '$lib/ui/patterns/EngineBadge.svelte';

	let {
		onSelectTable,
		search = $bindable('')
	}: {
		onSelectTable?: (t: TableRef | null) => void;
		search?: string;
	} = $props();

	const workspaceId = $derived(workspaceStore.current?.id ?? '');

	type View = 'catalog' | 'engine';
	let view = $state<View>('catalog');

	type NamespaceNode = {
		status: 'loading' | 'ready' | 'error';
		error: string;
		children: { path: string[] }[];
		tables: MetadataTableSummary[];
		nextCursor: string | null;
		tablesLoading: boolean;
		tablesError: string;
		freshness: MetadataFreshness | null;
	};

	function emptyNode(): NamespaceNode {
		return {
			status: 'loading',
			error: '',
			children: [],
			tables: [],
			nextCursor: null,
			tablesLoading: false,
			tablesError: '',
			freshness: null
		};
	}

	let catalogs = $state<MetadataCatalog[]>([]);
	let loading = $state(true);
	let loadError = $state('');

	// Every loaded node keyed by rendered ref + namespace path. The ref is the
	// catalog id in "by catalog" and `<engine>:<catalog>` in "by engine", so the
	// same catalog under two engines expands independently.
	let nodes = $state<Record<string, NamespaceNode>>({});
	const expanded = new SvelteSet<string>();
	const inflight = new SvelteMap<string, Promise<void>>();

	let selectedTableId = $state('');

	function nodeKey(refKey: string, path: string[]): string {
		return `${refKey}|${path.join('\u0000')}`;
	}

	function resetTree() {
		catalogs = [];
		nodes = {};
		expanded.clear();
		inflight.clear();
		selectedTableId = '';
		searchResults = [];
		searchError = '';
		searchLoading = false;
		activeResult = 0;
	}

	async function loadRoots(wid: string) {
		resetTree();
		if (!wid) {
			loading = false;
			return;
		}
		loading = true;
		loadError = '';
		try {
			catalogs = await loadCatalogs(wid);
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		void loadRoots(workspaceId);
	});

	function loadNode(node: NamespaceNode, catalog: MetadataCatalog, path: string[]): Promise<void> {
		const wid = workspaceId;
		node.status = 'loading';
		node.error = '';
		return loadNamespaces(wid, catalog.id, path).then((list) => {
			// Only deeper paths are children: a self-referential row would make
			// the recursive tree loop forever.
			node.children = list.items
				.map((item) => ({ path: item.path }))
				.filter((item) => item.path.length > path.length);
			node.freshness = list.freshness;
			// Namespaces render as soon as they arrive; tables load in the
			// background so expanding a level is one round trip, not two (#233).
			node.status = 'ready';
			// A root with namespaces has no tables of its own; an empty root
			// (SQLite) lists its tables without a namespace.
			if (path.length > 0 || node.children.length === 0) {
				void loadInitialTables(node, catalog, path);
			}
		});
	}

	/** Loads the first page of tables for a node, replacing whatever it had. */
	async function loadInitialTables(node: NamespaceNode, catalog: MetadataCatalog, path: string[]) {
		if (node.tablesLoading) return;
		node.tablesLoading = true;
		node.tablesError = '';
		try {
			const page = await loadTables(workspaceId, catalog.id, path);
			node.tables = page.items;
			node.nextCursor = page.next_cursor ?? null;
			node.freshness = page.freshness;
		} catch (e) {
			node.tablesError = e instanceof Error ? e.message : String(e);
		} finally {
			node.tablesLoading = false;
		}
	}

	function ensureLoaded(refKey: string, catalog: MetadataCatalog, path: string[]): Promise<void> {
		const key = nodeKey(refKey, path);
		const running = inflight.get(key);
		if (running) return running;
		if (!nodes[key]) nodes[key] = emptyNode();
		// Read through the state proxy: the assignment expression returns the
		// raw object, whose mutations would not be reactive.
		const node = nodes[key];
		if (node.status === 'ready') return Promise.resolve();
		const pending = loadNode(node, catalog, path)
			.catch((e) => {
				node.status = 'error';
				node.error = e instanceof Error ? e.message : String(e);
			})
			.finally(() => inflight.delete(key));
		inflight.set(key, pending);
		return pending;
	}

	function toggleNode(refKey: string, catalog: MetadataCatalog, path: string[]) {
		const key = nodeKey(refKey, path);
		if (expanded.has(key)) {
			expanded.delete(key);
			return;
		}
		expanded.add(key);
		void ensureLoaded(refKey, catalog, path);
	}

	async function loadMoreTables(refKey: string, catalog: MetadataCatalog, path: string[]) {
		const node = nodes[nodeKey(refKey, path)];
		if (!node || !node.nextCursor || node.tablesLoading) return;
		node.tablesLoading = true;
		node.tablesError = '';
		try {
			const page: MetadataTablePage = await loadTables(
				workspaceId,
				catalog.id,
				path,
				node.nextCursor
			);
			node.tables = [...node.tables, ...page.items];
			node.nextCursor = page.next_cursor ?? null;
		} catch (e) {
			node.tablesError = e instanceof Error ? e.message : String(e);
		} finally {
			node.tablesLoading = false;
		}
	}

	/** Retries the tables for a node: first page when empty, else the next page. */
	function retryTables(refKey: string, catalog: MetadataCatalog, path: string[]) {
		const node = nodes[nodeKey(refKey, path)];
		if (!node || node.tablesLoading) return;
		if (node.tables.length === 0) void loadInitialTables(node, catalog, path);
		else void loadMoreTables(refKey, catalog, path);
	}

	function selectTable(catalog: MetadataCatalog, path: string[], table: MetadataTableSummary) {
		selectedTableId = table.id;
		onSelectTable?.({
			tableId: table.id,
			catalogId: catalog.id,
			catalogName: catalog.name,
			namespace: path,
			tableName: table.name,
			bindings: catalog.bindings
		});
	}

	// ── Search ───────────────────────────────────────────────────────────
	let searchResults = $state<MetadataSearchResult[]>([]);
	let searchLoading = $state(false);
	let searchError = $state('');
	let activeResult = $state(0);
	let retryTick = $state(0);
	let searchSeq = 0;
	const searchActive = $derived(search.trim().length >= 2);

	$effect(() => {
		const q = search.trim();
		const wid = workspaceId;
		// Retry is driven by a tick so re-submitting the same query re-runs it.
		void retryTick;

		if (q.length < 2 || !wid) {
			searchResults = [];
			searchError = '';
			searchLoading = false;
			return;
		}

		const controller = new AbortController();
		searchLoading = true;
		searchError = '';
		activeResult = 0;
		const timer = setTimeout(async () => {
			const seq = ++searchSeq;
			try {
				const results = await searchMetadata(wid, q, {
					kind: 'all',
					limit: 50,
					signal: controller.signal
				});
				if (seq !== searchSeq) return;
				searchResults = results;
			} catch (e) {
				if (seq !== searchSeq || controller.signal.aborted) return;
				searchResults = [];
				searchError = e instanceof Error ? e.message : String(e);
			} finally {
				if (seq === searchSeq) searchLoading = false;
			}
		}, 250);

		return () => {
			clearTimeout(timer);
			controller.abort();
		};
	});

	const tableResults = $derived(searchResults.filter((r) => r.kind === 'table'));
	const columnResults = $derived(searchResults.filter((r) => r.kind === 'column'));
	const flatResults = $derived([...tableResults, ...columnResults]);

	/** Keyboard navigation from the sidebar search input (FD11). */
	export function handleSearchKey(event: KeyboardEvent) {
		if (!searchActive) return;
		if (event.key === 'ArrowDown') {
			event.preventDefault();
			activeResult = Math.min(activeResult + 1, flatResults.length - 1);
		} else if (event.key === 'ArrowUp') {
			event.preventDefault();
			activeResult = Math.max(activeResult - 1, 0);
		} else if (event.key === 'Enter') {
			event.preventDefault();
			const hit = flatResults[activeResult];
			if (hit) void reveal(hit);
		}
	}

	/** Expands the tree to a search hit and selects it. */
	async function reveal(hit: MetadataSearchResult) {
		view = 'catalog';
		const catalog = catalogs.find((c) => c.id === hit.catalog_id);
		if (!catalog) return;
		const refKey = catalog.id;
		expanded.add(nodeKey(refKey, []));
		await ensureLoaded(refKey, catalog, []);
		for (let depth = 1; depth <= hit.namespace.length; depth++) {
			const path = hit.namespace.slice(0, depth);
			expanded.add(nodeKey(refKey, path));
			await ensureLoaded(refKey, catalog, path);
		}
		selectTable(catalog, hit.namespace, {
			id: hit.table_id,
			name: hit.table,
			kind: 'table'
		});
		search = '';
	}

	// ── Engine grouping ──────────────────────────────────────────────────
	type EngineGroup = { key: string; name: string; kind: string; catalogs: MetadataCatalog[] };
	const engineGroups = $derived.by(() => {
		const groups: Record<string, EngineGroup> = {};
		for (const catalog of catalogs) {
			for (const binding of catalog.bindings) {
				let group = groups[binding.engine_id];
				if (!group) {
					group = {
						key: binding.engine_id,
						name: binding.engine_name,
						kind: binding.engine_kind,
						catalogs: []
					};
					groups[binding.engine_id] = group;
				}
				if (!group.catalogs.includes(catalog)) group.catalogs.push(catalog);
			}
		}
		return Object.values(groups);
	});
</script>

<div class="overflow-y-auto flex-1 px-1.5 pb-2 text-[13px]" data-testid="metadata-tree">
	{#if !workspaceStore.current}
		<p class="text-text-muted text-xs px-2 py-4">Select a workspace to browse catalogs.</p>
	{:else if loading}
		<div class="space-y-2 px-2 py-3" aria-label="Loading catalogs">
			<Skeleton class="h-4 w-3/4" />
			<Skeleton class="h-4 w-2/3" />
			<Skeleton class="h-4 w-4/5" />
		</div>
	{:else if loadError}
		<ErrorState
			title="Could not load catalogs"
			message={loadError}
			onretry={() => loadRoots(workspaceId)}
		/>
	{:else if searchActive}
		{#if searchLoading}
			<div class="space-y-2 px-2 py-3" aria-label="Searching">
				<Skeleton class="h-4 w-3/4" />
				<Skeleton class="h-4 w-2/3" />
			</div>
		{:else if searchError}
			<ErrorState title="Search failed" message={searchError} onretry={() => retryTick++} />
		{:else}
			<div role="listbox" aria-label="Search results">
				{#if tableResults.length > 0}
					<p class="px-2 pt-2 pb-1 text-[10px] font-bold uppercase tracking-wide text-text-muted">
						Tables
					</p>
					{#each tableResults as result (result.table_id + result.table)}
						{@render searchRow(result, flatResults.indexOf(result))}
					{/each}
				{/if}
				{#if columnResults.length > 0}
					<p class="px-2 pt-2 pb-1 text-[10px] font-bold uppercase tracking-wide text-text-muted">
						Columns
					</p>
					{#each columnResults as result (result.table_id + result.column)}
						{@render searchRow(result, flatResults.indexOf(result))}
					{/each}
				{/if}
				{#if tableResults.length === 0 && columnResults.length === 0}
					<EmptyState
						title="No matches"
						description="No table or column matches that search in the catalogs you can see."
					/>
				{/if}
			</div>
		{/if}
	{:else if catalogs.length === 0}
		<EmptyState
			title="No catalogs"
			description="This workspace has no granted catalogs. Ask an owner to bind one."
		/>
	{:else}
		<div class="flex gap-1 px-1.5 py-2" role="group" aria-label="Group catalogs">
			<button
				class="flex-1 rounded border px-2 py-1 text-[11px] font-medium {view === 'catalog'
					? 'border-primary bg-bg-panel text-primary'
					: 'border-border bg-transparent text-text-muted hover:text-text-main'}"
				aria-pressed={view === 'catalog'}
				onclick={() => (view = 'catalog')}>By catalog</button
			>
			<button
				class="flex-1 rounded border px-2 py-1 text-[11px] font-medium {view === 'engine'
					? 'border-primary bg-bg-panel text-primary'
					: 'border-border bg-transparent text-text-muted hover:text-text-main'}"
				aria-pressed={view === 'engine'}
				onclick={() => (view = 'engine')}>By engine</button
			>
		</div>

		{#if view === 'catalog'}
			{#each catalogs as catalog (catalog.id)}
				{@render catalogNode(catalog.id, catalog)}
			{/each}
		{:else}
			{#each engineGroups as group (group.key)}
				<div class="flex items-center gap-1.5 px-2 pt-2 pb-1">
					<EngineBadge engine={{ name: group.name, kind: group.kind }} />
				</div>
				{#each group.catalogs as catalog (group.key + ':' + catalog.id)}
					{@render catalogNode(group.key + ':' + catalog.id, catalog)}
				{/each}
			{/each}
		{/if}
	{/if}
</div>

{#snippet searchRow(result: MetadataSearchResult, flatIndex: number)}
	<button
		role="option"
		aria-selected={activeResult === flatIndex}
		class="flex w-full flex-col gap-0.5 rounded px-2 py-1.5 text-left hover:bg-slate-100 dark:hover:bg-[#1c2128] {activeResult ===
		flatIndex
			? 'bg-blue-50 text-primary dark:bg-[#152236]'
			: ''}"
		onclick={() => reveal(result)}
	>
		<span class="font-mono text-xs">
			{result.kind === 'column' ? `${result.table}.${result.column}` : result.table}
		</span>
		<span class="text-[10px] text-text-muted">
			{[result.namespace.join('.'), result.native_type].filter(Boolean).join(' · ')}
		</span>
	</button>
{/snippet}

{#snippet catalogNode(refKey: string, catalog: MetadataCatalog)}
	{@const key = nodeKey(refKey, [])}
	{@const open = expanded.has(key)}
	<button
		class="flex w-full items-center gap-1.5 rounded-[5px] px-1.5 py-[5px] text-left text-[13px] text-inherit hover:bg-slate-100 dark:hover:bg-[#1c2128]"
		onclick={() => toggleNode(refKey, catalog, [])}
		aria-expanded={open}
	>
		<svg
			viewBox="0 0 24 24"
			class="h-3.5 w-3.5 shrink-0 text-text-muted transition-transform duration-150 {open
				? 'rotate-90'
				: ''}"><path d="M9 18l6-6-6-6" /></svg
		>
		<svg viewBox="0 0 24 24" class="h-3.5 w-3.5 shrink-0 stroke-text-muted"
			><ellipse cx="12" cy="5" rx="9" ry="3" /><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" /><path
				d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"
			/></svg
		>
		<span class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap font-medium"
			>{catalog.name}</span
		>
		<span class="flex shrink-0 items-center gap-1">
			{#each catalog.bindings as binding (binding.binding_id)}
				<span
					class="rounded border border-border px-1 py-px text-[9px] uppercase text-text-muted"
					title={`${binding.engine_name} (${binding.mode})`}>{binding.engine_name}</span
				>
			{/each}
		</span>
	</button>
	{#if open}
		{@render namespaceNode(refKey, catalog, [])}
	{/if}
{/snippet}

{#snippet namespaceNode(refKey: string, catalog: MetadataCatalog, path: string[])}
	{@const node = nodes[nodeKey(refKey, path)]}
	<div class="pl-4">
		{#if !node || node.status === 'loading'}
			<p class="py-1 pl-2 text-[11px] text-text-muted">Loading…</p>
		{:else if node.status === 'error'}
			<ErrorState
				title="Could not load this level"
				message={node.error}
				onretry={() => {
					expanded.add(nodeKey(refKey, path));
					void ensureLoaded(refKey, catalog, path);
				}}
			/>
		{:else}
			{#each node.children as child (child.path.join('\u0000'))}
				{@const childKey = nodeKey(refKey, child.path)}
				{@const childOpen = expanded.has(childKey)}
				<button
					class="flex w-full items-center gap-1.5 rounded-[5px] px-1.5 py-[5px] text-left text-[13px] text-inherit hover:bg-slate-100 dark:hover:bg-[#1c2128]"
					onclick={() => toggleNode(refKey, catalog, child.path)}
					aria-expanded={childOpen}
				>
					<svg
						viewBox="0 0 24 24"
						class="h-3.5 w-3.5 shrink-0 text-text-muted transition-transform duration-150 {childOpen
							? 'rotate-90'
							: ''}"><path d="M9 18l6-6-6-6" /></svg
					>
					<svg viewBox="0 0 24 24" class="h-3.5 w-3.5 shrink-0 stroke-slate-400"
						><path
							d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"
						/></svg
					>
					<span
						class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-text-muted"
						>{child.path[child.path.length - 1]}</span
					>
				</button>
				{#if childOpen}
					{@render namespaceNode(refKey, catalog, child.path)}
				{/if}
			{/each}

			{#each node.tables as table (table.id)}
				<button
					class="flex w-full items-center gap-1.5 rounded-[5px] px-1.5 py-[5px] text-left text-[13px] text-inherit hover:bg-slate-100 dark:hover:bg-[#1c2128] {selectedTableId ===
					table.id
						? 'bg-blue-50 font-medium text-primary dark:bg-[#152236]'
						: ''}"
					onclick={() => selectTable(catalog, path, table)}
					aria-current={selectedTableId === table.id}
				>
					<svg
						viewBox="0 0 24 24"
						class="h-3.5 w-3.5 shrink-0 {selectedTableId === table.id
							? 'stroke-primary'
							: 'stroke-text-muted'}"
						><rect x="3" y="3" width="18" height="18" rx="2" /><line
							x1="3"
							y1="9"
							x2="21"
							y2="9"
						/><line x1="9" y1="21" x2="9" y2="9" /></svg
					>
					<span class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap"
						>{table.name}</span
					>
					{#if table.kind !== 'table'}
						<span class="rounded border border-border px-1 text-[9px] uppercase text-text-muted"
							>{table.kind}</span
						>
					{/if}
				</button>
			{/each}

			{#if node.tablesError}
				<ErrorState
					title="Could not load tables"
					message={node.tablesError}
					onretry={() => retryTables(refKey, catalog, path)}
				/>
			{:else if node.nextCursor}
				<button
					class="flex w-full items-center gap-1.5 rounded-[5px] px-1.5 py-[5px] pl-2 text-left text-[11px] font-medium text-primary hover:bg-slate-100 dark:hover:bg-[#1c2128] disabled:opacity-50"
					disabled={node.tablesLoading}
					onclick={() => loadMoreTables(refKey, catalog, path)}
				>
					{node.tablesLoading ? 'Loading…' : 'Load more'}
				</button>
			{/if}

			{#if node.tablesLoading && node.tables.length === 0}
				<p class="py-1 pl-2 text-[11px] text-text-muted">Loading tables…</p>
			{:else if node.children.length === 0 && node.tables.length === 0 && !node.nextCursor}
				<p class="py-1 pl-2 text-[11px] text-text-muted">No tables or namespaces.</p>
			{/if}
		{/if}
	</div>
{/snippet}
