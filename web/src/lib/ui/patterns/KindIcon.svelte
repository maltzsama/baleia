<script lang="ts">
	import { appearanceFor } from './kindIcon';

	let {
		kind,
		family,
		size = 16
	}: {
		kind: string;
		family?: string;
		size?: number;
	} = $props();

	const appearance = $derived(appearanceFor(kind, family));
</script>

<svg
	viewBox="0 0 24 24"
	width={size}
	height={size}
	class={appearance.color}
	aria-hidden="true"
	fill="none"
	stroke="currentColor"
	stroke-width="2"
	stroke-linecap="round"
	stroke-linejoin="round"
>
	{#if appearance.family === 'file'}
		<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
		<path d="M14 2v6h6" />
	{:else if appearance.family === 'metastore'}
		<path d="M3 7h18M3 12h18M3 17h18" />
		<path d="M7 3v18M17 3v18" />
	{:else if appearance.family === 'engine'}
		<circle cx="12" cy="12" r="3" />
		<path d="M19 9a7 7 0 0 1 0 6M5 15a7 7 0 0 1 0-6M12 2v2M12 20v2" />
	{:else}
		<ellipse cx="12" cy="5" rx="9" ry="3" />
		<path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" />
		<path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" />
	{/if}
</svg>
