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

	// A dark mark gets a light chip in dark mode so it stays legible; a light one
	// is drawn in a darker shade of its colour in light mode (see .kind-mark).
	const chip = $derived(
		appearance.contrast ? 'dark:bg-white/90 dark:p-px dark:ring-1 dark:ring-black/10' : ''
	);
</script>

{#if appearance.mark || appearance.brand}
	<span
		class="inline-flex shrink-0 items-center justify-center rounded-[3px] {chip} {className}"
		style:width={`${size}px`}
		style:height={`${size}px`}
		aria-hidden="true"
	>
		{#if appearance.mark}
			<svg
				viewBox="0 0 24 24"
				class="kind-mark h-full w-full"
				style:--mark={`#${appearance.mark.hex}`}
				style:--mark-light={`#${appearance.lightHex ?? appearance.mark.hex}`}
			>
				<path d={appearance.mark.path} />
			</svg>
		{:else}
			<img
				src={appearance.brand}
				alt=""
				class="h-full w-full object-contain"
				draggable="false"
				loading="lazy"
			/>
		{/if}
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
		{:else if appearance.glyph === 'memory'}
			<rect x="6" y="6" width="12" height="12" rx="1" />
			<path d="M9 2v4m6-4v4M9 18v4m6-4v4M2 9h4m-4 6h4m12-6h4m-4 6h4" />
		{:else if appearance.glyph === 'gauge'}
			<path d="M4 18a9 9 0 1 1 16 0" />
			<path d="m12 14 4-5" />
			<circle cx="12" cy="15" r="1" />
		{:else if appearance.glyph === 'bars'}
			<path d="M4 20V10m6 10V4m6 16v-7m4 7H2" />
		{:else if appearance.glyph === 'void'}
			<circle cx="12" cy="12" r="9" />
			<circle cx="12" cy="12" r="4" />
			<circle cx="12" cy="12" r="0.5" />
		{:else if appearance.glyph === 'sliders'}
			<path d="M4 7h10m4 0h2M4 17h2m4 0h10" />
			<circle cx="16" cy="7" r="2" />
			<circle cx="8" cy="17" r="2" />
		{:else}
			<ellipse cx="12" cy="5" rx="9" ry="3" />
			<path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" />
			<path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" />
		{/if}
	</svg>
{/if}

<style>
	/* app.css gives every svg a stroke and no fill: a brand mark is a filled
	   silhouette, in its own colour on the dark theme and in --mark-light on the
	   light one. */
	.kind-mark {
		fill: var(--mark-light);
		stroke: none;
	}
	:global(.dark) .kind-mark {
		fill: var(--mark);
	}
</style>
