<script lang="ts">
	import { onMount } from 'svelte';
	import { formatAbsoluteTime, formatRelativeTime } from '$lib/util/time';

	let { at, live = false }: { at: string | Date; live?: boolean } = $props();

	let now = $state(Date.now());

	onMount(() => {
		if (!live) return;
		const timer = setInterval(() => (now = Date.now()), 30_000);
		return () => clearInterval(timer);
	});

	const relative = $derived(formatRelativeTime(at, now));
	const absolute = $derived(formatAbsoluteTime(at));
</script>

<time datetime={at instanceof Date ? at.toISOString() : at} title={absolute}>{relative}</time>
