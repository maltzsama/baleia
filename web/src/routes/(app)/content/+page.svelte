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

	async function search() {
		if (!workspaceId || query.trim().length < 2) {
			results = [];
			return;
		}
		searching = true;
		searchError = '';
		try {
			results = await searchContent(workspaceId, query.trim(), { limit: 50 });
		} catch (e) {
			searchError = e instanceof Error ? e.message : String(e);
		} finally {
			searching = false;
		}
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
	{#if searchError}
		<p class="mb-3 text-[11px] text-danger" role="alert">{searchError}</p>
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
			class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3"
			role="status"
			aria-label={m.content_loading_spaces()}
		>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-28 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title="Could not load content" message={error} onretry={load} />
	{:else if spaces.length === 0}
		<EmptyState
			title="No spaces"
			description="Create a space to organize saved queries and metrics."
		/>
	{:else}
		<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
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
						{#if space.is_default}<Badge tone="neutral">Default</Badge>{/if}
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
						{space.counts?.saved_queries ?? 0} queries · {space.counts?.semantic_metrics ?? 0} metrics
						·
						{space.counts?.semantic_models ?? 0} models ·
						{space.counts?.folders ?? 0} folders
						{#if space.updated_at}· updated <RelativeTime at={space.updated_at} />{/if}
					</div>
				</article>
			{/each}
		</div>
	{/if}
</div>

<SpaceDialog bind:open={spaceOpen} ondone={() => workspaceId && savedQueries.reload(workspaceId)} />
