<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { plural } from '$lib/i18n/plural';
	import { popularTables, type PopularTable } from '$lib/metadata/client';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	let {
		onselect
	}: {
		onselect: (catalogId: string, namespace: string[], tableId: string, name: string) => void;
	} = $props();

	let rows = $state<PopularTable[]>([]);
	let loading = $state(true);
	let error = $state('');

	$effect(() => {
		const ws = workspaceStore.current?.id ?? '';
		if (!ws) return;
		let cancelled = false;
		loading = true;
		error = '';
		void popularTables(ws, 10)
			.then((data) => {
				if (!cancelled) rows = data;
			})
			.catch((e) => {
				if (!cancelled) error = e instanceof Error ? e.message : String(e);
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});
		return () => {
			cancelled = true;
		};
	});
</script>

<section aria-label={m.data_popular_in_this_workspace()} class="flex flex-col gap-2">
	<div>
		<h2 class="text-sm font-semibold">{m.data_popular_in_this_workspace()}</h2>
		<p class="text-[11px] text-text-muted">{m.data_popular_description()}</p>
	</div>
	{#if loading}
		<div class="space-y-2" role="status" aria-label={m.data_popular_in_this_workspace()}>
			<Skeleton class="h-9 w-full" />
			<Skeleton class="h-9 w-5/6" />
		</div>
	{:else if error}
		<ErrorState title={m.data_popular_failed()} message={error} />
	{:else if rows.length === 0}
		<div class="rounded-md border border-dashed border-border px-4">
			<EmptyState
				align="start"
				title={m.data_popular_empty()}
				description={m.data_browse_the_metadata_driven_catalogs_on()}
			/>
		</div>
	{:else}
		<ul class="flex flex-col gap-1">
			{#each rows as row (row.table_id)}
				<li>
					<button
						type="button"
						class="flex w-full items-center gap-2 rounded-md border border-border px-3 py-2 text-left text-xs hover:border-primary"
						onclick={() => onselect(row.catalog_id, row.namespace, row.table_id, row.name)}
					>
						<span class="min-w-0 flex-1 truncate font-mono">{row.name}</span>
						<span class="hidden text-[11px] text-text-muted sm:inline">
							{[...row.namespace, row.name].join('.')}
						</span>
						<span class="text-[11px] text-text-muted">
							{plural(row.uses, m.data_popular_uses_one, m.data_popular_uses_other)}
						</span>
						<RelativeTime at={row.last_used_at} />
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</section>
