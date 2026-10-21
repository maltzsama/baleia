<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Badge from '$lib/ui/Badge.svelte';
	import type { FieldGroup, FieldNode } from './explore';

	let {
		groups,
		selected,
		ontoggle
	}: {
		groups: FieldGroup[];
		selected: string[];
		ontoggle: (field: FieldNode) => void;
	} = $props();
</script>

{#snippet fieldButton(field: FieldNode)}
	<li>
		<button
			type="button"
			class="flex w-full items-center justify-between gap-2 rounded px-2 py-1 text-left text-[12px] hover:bg-bg-toolbar {selected.includes(
				field.name
			)
				? 'bg-bg-toolbar font-semibold text-primary'
				: ''}"
			aria-pressed={selected.includes(field.name)}
			onclick={() => ontoggle(field)}
		>
			<span class="truncate">{field.label}</span>
			<span class="shrink-0 font-mono text-[10px] text-text-muted">
				{field.kind === 'dimension' ? field.type : field.kind}
			</span>
		</button>
		{#if field.grains.length > 0}
			<div class="px-2 pb-1 text-[10px] text-text-muted">
				{m.explore_grains({ grains: field.grains.join(' · ') })}
			</div>
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
