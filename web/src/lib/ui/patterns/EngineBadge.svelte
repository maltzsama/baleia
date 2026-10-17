<script lang="ts">
	import KindIcon from './KindIcon.svelte';

	let {
		engine
	}: {
		engine: { name: string; kind: string; class?: string };
	} = $props();
</script>

<span class="inline-flex items-center gap-1.5 whitespace-nowrap text-xs text-text-main">
	<KindIcon kind={engine.kind} family="engine" />
	<span class="font-medium">{engine.name}</span>
	{#if engine.class === 'federated' || engine.class === 'direct'}
		<span
			class="rounded-full border border-border px-1.5 py-px text-[9px] uppercase text-text-muted"
		>
			{engine.class}
		</span>
	{/if}
</span>
