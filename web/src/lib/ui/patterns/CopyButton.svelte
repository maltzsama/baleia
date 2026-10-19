<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { toasts } from '../toast.svelte';

	let { value, label = 'value' }: { value: string; label?: string } = $props();

	let copied = $state(false);

	async function copy() {
		try {
			await navigator.clipboard.writeText(value);
			copied = true;
			toasts.success(m.components_resultgrid_copied_to_clipboard());
			setTimeout(() => (copied = false), 1500);
		} catch {
			toasts.error(m.components_resultgrid_could_not_copy_to_the_clipboard());
		}
	}
</script>

<button
	type="button"
	class="inline-flex items-center gap-1 rounded px-1 text-text-muted transition-colors hover:text-text-main"
	aria-label={`Copy ${label}`}
	title={`Copy ${label}`}
	onclick={copy}
>
	{#if copied}
		<svg viewBox="0 0 24 24" class="h-3.5 w-3.5" aria-hidden="true"
			><polyline points="20 6 9 17 4 12" /></svg
		>
	{:else}
		<svg viewBox="0 0 24 24" class="h-3.5 w-3.5" aria-hidden="true"
			><rect x="9" y="9" width="13" height="13" rx="2" /><path
				d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"
			/></svg
		>
	{/if}
</button>
