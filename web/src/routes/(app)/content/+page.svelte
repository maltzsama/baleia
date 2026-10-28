<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { contentHref, searchContent, type ContentSearchItem } from '$lib/content/client';
	import { kindEntry } from '$lib/content/kinds';
	import SpaceDialog from '$lib/components/SpaceDialog.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const spaces = $derived(savedQueries.spaces);
	const loading = $derived(savedQueries.loading);
	const error = $derived(savedQueries.error);

	let spaceOpen = $state(false);
	let query = $state('');
	let searching = $state(false);
	let searchError = $state('');
	let results = $state<ContentSearchItem[]>([]);

	async function load() {
		await workspaceStore.load();
		if (workspaceId) await savedQueries.reload(workspaceId);
	}

	// The search has its own states (#476), apart from the spaces list: too
	// short, searching, results, no results and error.
	let searchedFor = $state('');
	let tooShort = $state(false);

	async function search() {
		const q = query.trim();
		searchError = '';
		tooShort = q.length > 0 && q.length < 2;
		if (!workspaceId || q.length < 2) {
			results = [];
			searchedFor = '';
			return;
		}
		searching = true;
		try {
			results = await searchContent(workspaceId, q, { limit: 50 });
			searchedFor = q;
		} catch (e) {
			results = [];
			searchedFor = '';
			searchError = e instanceof Error ? e.message : String(e);
		} finally {
			searching = false;
		}
	}

	function clearSearch() {
		query = '';
		results = [];
		searchedFor = '';
		searchError = '';
		tooShort = false;
	}

	function openSpace(id: string) {
		window.location.href = `/content/spaces/${id}`;
	}

	function openItem(item: ContentSearchItem) {
		window.location.href = contentHref(item);
	}

	function kindLabel(kind: string): string {
		return kindEntry(kind).label;
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_content()}</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{m.content_spaces_folders_saved_queries_and_metrics_a()}
			</p>
		</div>
		<Button variant="primary" onclick={() => (spaceOpen = true)}>{m.content_new_space()}</Button>
	</div>

	<div class="mb-4 flex flex-wrap items-end gap-2">
		<div class="min-w-64 flex-1">
			<Input
				label={m.content_search_content()}
				bind:value={query}
				placeholder={m.common_orders()}
			/>
		</div>
		<Button loading={searching} disabled={searching} onclick={search}>{m.common_search()}</Button>
	</div>
	<div role="status" class="contents">
		{#if tooShort}
			<p class="mb-3 text-[11px] text-slate-600 dark:text-slate-300">
				{m.content_type_at_least_two_characters()}
			</p>
		{:else if searchedFor && !searching && results.length === 0 && !searchError}
			<div class="mb-5 rounded-lg border border-border bg-bg-panel p-3 text-[12px]">
				<p>{m.content_no_content_matches({ query: searchedFor })}</p>
				<Button size="sm" class="mt-2" onclick={clearSearch}>{m.common_clear_search()}</Button>
			</div>
		{/if}
	</div>
	{#if searchError}
		<div class="mb-3">
			<ErrorState title={m.content_search_failed()} message={searchError} onretry={search} />
		</div>
	{/if}
	{#if results.length > 0}
		<div class="mb-5 rounded-lg border border-border bg-bg-panel p-3">
			<h2 class="text-[12px] font-semibold">{m.common_results()}</h2>
			<ul class="mt-2 flex flex-col gap-1 text-[11px]">
				{#each results as item (item.kind + item.id)}
					<li>
						<button type="button" class="text-left hover:underline" onclick={() => openItem(item)}>
							<Badge tone="neutral">{kindLabel(item.kind)}</Badge>
							<span class="ml-1 font-medium">{item.title}</span>
							<span class="ml-1 text-slate-600 dark:text-slate-300">
								· {item.space.name}{#if item.folder_path.length > 0}
									· {item.folder_path.map((folder) => folder.name).join(' / ')}{/if}
							</span>
						</button>
					</li>
				{/each}
			</ul>
		</div>
	{/if}

	{#if loading}
		<div
			class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,320px),1fr))] gap-3"
			role="status"
			aria-label={m.content_loading_spaces()}
		>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-28 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title={m.content_could_not_load_content()} message={error} onretry={load} />
	{:else if spaces.length === 0}
		<EmptyState
			title={m.content_no_spaces()}
			description={m.content_create_a_space_to_organize_saved()}
		/>
	{:else}
		<div class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,320px),1fr))] gap-3">
			{#each spaces as space (space.id)}
				<article class="flex flex-col gap-2 rounded-lg border border-border bg-bg-panel p-4">
					<div class="flex items-start justify-between gap-2">
						<button
							type="button"
							class="text-left text-[13px] font-semibold hover:underline"
							onclick={() => openSpace(space.id)}
						>
							{space.name}
						</button>
						{#if space.is_default}<Badge tone="neutral">{m.content_default()}</Badge>{/if}
					</div>
					<p class="line-clamp-2 text-[11px] text-slate-600 dark:text-slate-300">
						{space.description ?? m.content_no_description()}
					</p>
					<div class="flex flex-wrap gap-1">
						<Badge tone={space.visibility === 'workspace' ? 'info' : 'warning'}>
							{space.visibility === 'workspace'
								? m.content_everyone_in_the_workspace()
								: m.content_restricted()}
						</Badge>
						{#if space.my_role}<Badge tone="success">{space.my_role}</Badge>{/if}
					</div>
					<div class="text-[10px] text-slate-600 dark:text-slate-300">
						{m.content_queries_metrics_models_folders({
							value: space.counts?.saved_queries ?? 0,
							value2: space.counts?.semantic_metrics ?? 0,
							value3: space.counts?.semantic_models ?? 0,
							value4: space.counts?.folders ?? 0
						})}
						{#if space.updated_at}{m.content_updated()} <RelativeTime at={space.updated_at} />{/if}
					</div>
				</article>
			{/each}
		</div>
	{/if}
</div>

<SpaceDialog bind:open={spaceOpen} ondone={() => workspaceId && savedQueries.reload(workspaceId)} />
