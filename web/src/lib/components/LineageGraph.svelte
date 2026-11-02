<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { resolve } from '$app/paths';
	import { client } from '$lib/api/client';
	import { contentKinds } from '$lib/content/kinds';
	import type { components } from '$lib/api/schema';
	import Badge from '$lib/ui/Badge.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';

	type LineageGraph = components['schemas']['LineageGraph'];
	type LineageNodeKind = components['schemas']['LineageNodeKind'];

	let {
		workspaceId,
		node,
		direction = 'down'
	}: {
		workspaceId: string;
		node: string;
		direction?: 'up' | 'down';
	} = $props();

	let loading = $state(true);
	let error = $state('');
	let graph = $state<LineageGraph | null>(null);

	$effect(() => {
		const ws = workspaceId;
		const target = node;
		const dir = direction;
		if (!ws || !target) {
			loading = false;
			graph = null;
			return;
		}
		let cancelled = false;
		loading = true;
		error = '';
		graph = null;
		client
			.GET('/workspaces/{id}/lineage', {
				params: { path: { id: ws }, query: { node: target, direction: dir } }
			})
			.then(({ data, error: err }) => {
				if (cancelled) return;
				if (err || !data) error = m.components_lineage_failed();
				else graph = data;
			})
			.catch(() => {
				if (!cancelled) error = m.components_lineage_failed();
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});
		return () => {
			cancelled = true;
		};
	});

	// The start node is already on screen where the section lives, so only its
	// neighbours are listed. The depth-1 graph is just the node and them.
	const neighbors = $derived((graph?.nodes ?? []).filter((item) => item.id !== node));

	function kindLabel(kind: LineageNodeKind): string {
		switch (kind) {
			case 'model':
				return contentKinds.semantic_model.label;
			case 'metric':
				return contentKinds.semantic_metric.label;
			case 'saved_query':
				return contentKinds.saved_query.label;
			default:
				return m.components_lineage_table();
		}
	}
</script>

<div class="text-[12px]" data-testid="lineage">
	<p class="mb-2 text-[10px] font-bold uppercase tracking-wide text-text-muted">
		{direction === 'up' ? m.components_lineage_used_by() : m.components_lineage_depends_on()}
	</p>

	{#if loading}
		<div class="space-y-2">
			<Skeleton class="h-4 w-2/3" />
			<Skeleton class="h-4 w-1/2" />
		</div>
	{:else if error}
		<ErrorState title={m.components_lineage_failed()} message={error} />
	{:else if neighbors.length === 0}
		<EmptyState title={m.components_lineage_empty()} />
	{:else}
		<ul class="space-y-1">
			{#each neighbors as item (item.id)}
				<li>
					{#if item.href}
						<a
							href={resolve(item.href)}
							class="flex items-center gap-2 rounded px-1.5 py-1 hover:bg-blue-50 hover:text-primary dark:hover:bg-[#152236]"
						>
							<span
								class="inline-flex h-1.5 w-1.5 shrink-0 rounded-full bg-primary"
								aria-hidden="true"
							></span>
							<span class="min-w-0 flex-1 truncate font-medium">{item.label}</span>
							<Badge tone="neutral">{kindLabel(item.kind)}</Badge>
						</a>
					{:else}
						<div class="flex items-center gap-2 px-1.5 py-1">
							<span
								class="inline-flex h-1.5 w-1.5 shrink-0 rounded-full bg-text-muted"
								aria-hidden="true"
							></span>
							<span class="min-w-0 flex-1 truncate font-mono">{item.label}</span>
							{#if item.detail}
								<span class="truncate font-mono text-[11px] text-text-muted">{item.detail}</span>
							{/if}
						</div>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>
