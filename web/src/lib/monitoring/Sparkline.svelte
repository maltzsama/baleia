<script lang="ts">
	// A decorative trend line for a card (#820): the values are already shown
	// next to it, so the drawing stays out of the accessibility tree.
	let { points, height = 24 }: { points: number[]; height?: number } = $props();

	const WIDTH = 100;
	const top = $derived(Math.max(1, ...points));
	const path = $derived(
		points
			.map((value, i) => {
				const x = (i / Math.max(1, points.length - 1)) * WIDTH;
				const y = height - (value / top) * height;
				return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`;
			})
			.join(' ')
	);
</script>

<svg
	viewBox="0 0 {WIDTH} {height}"
	preserveAspectRatio="none"
	class="h-6 w-full text-primary"
	aria-hidden="true"
>
	<path d={path} fill="none" stroke="currentColor" stroke-width="1.5" />
</svg>
