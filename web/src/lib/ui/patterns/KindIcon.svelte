<script lang="ts">
	import { appearanceFor } from './kindIcon';

	let {
		kind,
		family,
		size = 16,
		class: className = ''
	}: {
		kind: string;
		family?: string;
		size?: number;
		class?: string;
	} = $props();

	const appearance = $derived(appearanceFor(kind, family));
</script>

{#if appearance.brand}
	<span
		class="brand inline-flex shrink-0 items-center justify-center {className}"
		style:width={`${size}px`}
		style:height={`${size}px`}
		aria-hidden="true"
	>
		<!-- eslint-disable-next-line svelte/no-at-html-tags -- vendored, static SVG asset -->
		{@html appearance.brand}
	</span>
{:else}
	<svg
		viewBox="0 0 24 24"
		class="shrink-0 {appearance.color} {className}"
		style:width={`${size}px`}
		style:height={`${size}px`}
		aria-hidden="true"
		fill="none"
		stroke="currentColor"
		stroke-width="2"
		stroke-linecap="round"
		stroke-linejoin="round"
	>
		{#if appearance.glyph === 'file'}
			<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
			<path d="M14 2v6h6" />
		{:else if appearance.glyph === 'metastore'}
			<path d="M3 7h18M3 12h18M3 17h18" />
			<path d="M7 3v18M17 3v18" />
		{:else if appearance.glyph === 'engine'}
			<circle cx="12" cy="12" r="3" />
			<path d="M19 9a7 7 0 0 1 0 6M5 15a7 7 0 0 1 0-6M12 2v2M12 20v2" />
		{:else if appearance.glyph === 'mountain'}
			<path d="M12 2 2 21h20z" />
			<path d="M5 15h14" />
		{:else if appearance.glyph === 'nodes'}
			<circle cx="12" cy="5" r="2" />
			<circle cx="5" cy="19" r="2" />
			<circle cx="19" cy="19" r="2" />
			<path d="M12 7v5m-4 3 4-4m8 4-4-4" />
		{:else if appearance.glyph === 'waves'}
			<path d="M2 13c2.5-3 5-3 7.5 0s5 3 7.5 0 3-2 5-1" />
			<path d="M2 18c2.5-3 5-3 7.5 0s5 3 7.5 0 3-2 5-1" />
		{:else}
			<ellipse cx="12" cy="5" rx="9" ry="3" />
			<path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" />
			<path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" />
		{/if}
	</svg>
{/if}

<style>
	/* The app-wide `svg` rule pins 14px and a currentColor outline for inline
	   line icons. A brand mark is a filled, multi-colour SVG: restore its
	   original fills and sizing here. */
	.brand :global(svg) {
		display: block;
		width: 100%;
		height: 100%;
		stroke: none;
	}
</style>
