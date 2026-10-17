<script lang="ts">
	import { formatDuration } from '$lib/util/time';

	let { ms }: { ms: number } = $props();
</script>

<span class="font-mono text-xs text-text-muted">{formatDuration(ms)}</span>
