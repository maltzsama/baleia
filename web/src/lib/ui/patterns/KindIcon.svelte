<script lang="ts">
	import ChartNoAxesColumnIcon from '@lucide/svelte/icons/chart-no-axes-column';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import FileIcon from '@lucide/svelte/icons/file';
	import GaugeIcon from '@lucide/svelte/icons/gauge';
	import Grid3x3Icon from '@lucide/svelte/icons/grid-3x3';
	import LibraryIcon from '@lucide/svelte/icons/library';
	import MemoryStickIcon from '@lucide/svelte/icons/memory-stick';
	import MountainIcon from '@lucide/svelte/icons/mountain';
	import NetworkIcon from '@lucide/svelte/icons/network';
	import SlidersHorizontalIcon from '@lucide/svelte/icons/sliders-horizontal';
	import TargetIcon from '@lucide/svelte/icons/target';
	import WavesHorizontalIcon from '@lucide/svelte/icons/waves-horizontal';
	import { appearanceFor, type KindGlyph } from './kindIcon';

	let {
		kind,
		family,
		size = 32,
		class: className = ''
	}: {
		kind: string;
		family?: string;
		size?: number;
		class?: string;
	} = $props();

	const appearance = $derived(appearanceFor(kind, family));

	// The line glyph of a kind with no brand mark.
	const glyphs: Record<KindGlyph, typeof DatabaseIcon> = {
		catalog: LibraryIcon,
		metastore: Grid3x3Icon,
		database: DatabaseIcon,
		engine: CpuIcon,
		file: FileIcon,
		mountain: MountainIcon,
		nodes: NetworkIcon,
		waves: WavesHorizontalIcon,
		memory: MemoryStickIcon,
		gauge: GaugeIcon,
		bars: ChartNoAxesColumnIcon,
		void: TargetIcon,
		sliders: SlidersHorizontalIcon
	};

	// A vendored dark mark (an image, so it cannot be recoloured) gets a light chip
	// in dark mode. A package mark is recoloured per theme instead (see .kind-mark).
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
				style:--mark-dark={`#${appearance.darkHex ?? appearance.mark.hex}`}
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
	{@const Glyph = glyphs[appearance.glyph]}
	<Glyph
		class="shrink-0 {appearance.color} {className}"
		style="width: {size}px; height: {size}px"
		aria-hidden="true"
	/>
{/if}

<style>
	/* app.css gives every svg a stroke and no fill: a brand mark is a filled
	   silhouette, in --mark-light on the light theme and --mark-dark on the dark
	   one (its own colour unless that is hard to see there). */
	.kind-mark {
		fill: var(--mark-light);
		stroke: none;
	}
	:global(.dark) .kind-mark {
		fill: var(--mark-dark);
	}
</style>
