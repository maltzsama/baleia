<script lang="ts">
	import { dev } from '$app/environment';
	import type { Component } from 'svelte';

	// Dynamically imported so the examples never ship in the production bundle.
	let Examples = $state<Component | null>(null);

	$effect(() => {
		if (!dev) return;
		void import('$lib/ui/patterns/patterns.examples.svelte').then(
			(mod) => (Examples = mod.default)
		);
	});
</script>

{#if dev && Examples}
	<Examples />
{/if}
