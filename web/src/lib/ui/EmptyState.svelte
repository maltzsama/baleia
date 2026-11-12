<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		align = 'center',
		children
	}: {
		title: string;
		description?: string;
		/** `start` keeps the text on the left edge, under a left-aligned heading. */
		align?: 'center' | 'start';
		children?: Snippet;
	} = $props();
</script>

<div
	class="flex flex-col gap-2 {align === 'start'
		? 'items-start py-4 text-left'
		: 'items-center justify-center px-6 py-10 text-center'}"
>
	<div class="text-[15px] font-semibold text-text-main">{title}</div>
	{#if description}
		<p class="max-w-md text-[13px] leading-[1.55] text-text-muted">{description}</p>
	{/if}
	{#if children}
		<div class="mt-2">{@render children()}</div>
	{/if}
</div>
