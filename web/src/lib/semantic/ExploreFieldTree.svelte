<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { SvelteSet } from 'svelte/reactivity';
	import Badge from '$lib/ui/Badge.svelte';
	import type { FieldGroup, FieldNode } from './explore';

	let {
		groups,
		selected,
		timeGrain = '',
		ontoggle,
		onpickgrain
	}: {
		groups: FieldGroup[];
		selected: string[];
		timeGrain?: string;
		ontoggle: (field: FieldNode) => void;
		/** A time dimension's grain: selects the dimension at that grain. */
		onpickgrain: (field: FieldNode, grain: string) => void;
	} = $props();

	// Time dimensions whose grains are shown.
	const expanded = new SvelteSet<string>();

	function toggleExpanded(name: string) {
		if (expanded.has(name)) expanded.delete(name);
		else expanded.add(name);
	}
</script>

{#snippet fieldButton(field: FieldNode)}
	<li>
		<div class="flex items-center gap-0.5">
			{#if field.grains.length > 0}
				<button
					type="button"
					class="w-5 shrink-0 rounded text-[10px] text-text-muted hover:bg-bg-toolbar"
					aria-expanded={expanded.has(field.name)}
					aria-label={m.explore_show_grains({ field: field.label })}
					onclick={() => toggleExpanded(field.name)}
				>
					{expanded.has(field.name) ? '▾' : '▸'}
				</button>
			{/if}
			<button
				type="button"
				class="flex min-w-0 flex-1 items-center justify-between gap-2 rounded px-2 py-1 text-left text-[12px] hover:bg-bg-toolbar disabled:cursor-not-allowed disabled:line-through {selected.includes(
					field.name
				)
					? 'bg-bg-toolbar font-semibold text-primary'
					: ''}"
				aria-pressed={selected.includes(field.name)}
				disabled={field.ambiguous}
				title={field.ambiguous ? m.explore_ambiguous_field() : field.description || undefined}
				onclick={() => ontoggle(field)}
			>
				<span class="truncate">{field.label}</span>
				{#if field.ambiguous}
					<span class="sr-only">{m.explore_ambiguous_field()}</span>
				{/if}
				<span class="shrink-0 font-mono text-[10px] text-text-muted">
					{field.kind === 'dimension' ? field.type : field.kind}
				</span>
			</button>
		</div>
		{#if field.grains.length > 0 && expanded.has(field.name)}
			<ul
				class="ml-6 flex flex-wrap gap-1 py-1"
				aria-label={m.explore_grains_of({ field: field.label })}
			>
				{#each field.grains as grain (grain)}
					{@const active = selected.includes(field.name) && timeGrain === grain}
					<li>
						<button
							type="button"
							class="rounded border px-1.5 py-0.5 text-[10px] {active
								? 'border-primary bg-primary text-white'
								: 'border-border hover:bg-bg-toolbar'}"
							aria-pressed={active}
							onclick={() => onpickgrain(field, grain)}
						>
							{grain}
						</button>
					</li>
				{/each}
			</ul>
		{/if}
	</li>
{/snippet}

<div class="flex flex-col gap-3" data-testid="field-tree">
	{#each groups as group (group.model)}
		<section aria-label={group.label}>
			<h3 class="mb-1 flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wide">
				{group.label}
				{#if group.join}<Badge tone="info">{m.explore_joined()}</Badge>{/if}
			</h3>
			{#if group.dimensions.length > 0}
				<div class="px-2 text-[10px] font-semibold text-text-muted">{m.explore_dimensions()}</div>
				<ul>
					{#each group.dimensions as field (field.name)}
						{@render fieldButton(field)}
					{/each}
				</ul>
			{/if}
			{#if group.metrics.length > 0}
				<div class="mt-1 px-2 text-[10px] font-semibold text-text-muted">{m.explore_metrics()}</div>
				<ul>
					{#each group.metrics as field (field.name)}
						{@render fieldButton(field)}
					{/each}
				</ul>
			{/if}
		</section>
	{/each}
</div>
