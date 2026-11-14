<script lang="ts">
	import CheckIcon from '@lucide/svelte/icons/check';
	import CopyIcon from '@lucide/svelte/icons/copy';
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
	aria-label={m.copybutton_copy({ label })}
	title={m.copybutton_copy({ label })}
	onclick={copy}
>
	{#if copied}
		<CheckIcon class="h-3.5 w-3.5" aria-hidden="true" />
	{:else}
		<CopyIcon class="h-3.5 w-3.5" aria-hidden="true" />
	{/if}
</button>
