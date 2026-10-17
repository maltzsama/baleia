<script lang="ts">
	import type { Snippet } from 'svelte';

	type Tone = 'neutral' | 'info' | 'success' | 'warning' | 'danger';

	let {
		tone = 'neutral',
		children
	}: {
		tone?: Tone;
		children: Snippet;
	} = $props();

	const tones: Record<Tone, string> = {
		neutral: 'bg-bg-toolbar text-text-muted border-border',
		info: 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-[#152236] dark:text-blue-300 dark:border-[#1e3a5f]',
		success:
			'bg-green-50 text-green-700 border-green-200 dark:bg-[#0f231a] dark:text-green-300 dark:border-[#14532d]',
		warning:
			'bg-amber-50 text-amber-700 border-amber-200 dark:bg-[#241a06] dark:text-amber-300 dark:border-[#4d3600]',
		danger:
			'bg-red-50 text-red-700 border-red-200 dark:bg-[#1f0f0f] dark:text-red-300 dark:border-[#4d1111]'
	};
</script>

<span
	class="inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 text-[10px] font-semibold {tones[
		tone
	]}"
>
	{@render children()}
</span>
