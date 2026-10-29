<script lang="ts">
	import { base } from '$app/paths';

	let { class: className = 'h-7 w-7' }: { class?: string } = $props();
</script>

<!-- Decorative: the surrounding link or heading carries the Baleia name. -->
<img src="{base}/baleia.svg" alt="" class={className} />
