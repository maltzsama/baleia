<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		children
	}: {
		title: string;
		description?: string;
		children?: Snippet;
	} = $props();
</script>

<div class="flex flex-col items-center justify-center gap-2 px-6 py-10 text-center">
	<div class="text-[15px] font-semibold text-text-main">{title}</div>
	{#if description}
		<p class="max-w-md text-[13px] leading-[1.55] text-text-muted">{description}</p>
	{/if}
	{#if children}
		<div class="mt-2">{@render children()}</div>
	{/if}
</div>
