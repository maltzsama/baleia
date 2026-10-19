<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	let {
		title = m.ui_errorstate_something_went_wrong(),
		message,
		onretry
	}: {
		title?: string;
		message: string;
		onretry?: () => void;
	} = $props();
</script>

<div
	class="flex flex-col items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-red-700 dark:border-[#4d1111] dark:bg-[#1f0f0f] dark:text-red-300"
	role="alert"
>
	<div class="text-[13px] font-semibold">{title}</div>
	<p class="text-[12px] leading-[1.5]">{message}</p>
	{#if onretry}
		<button
			type="button"
			class="mt-1 h-7 rounded-md border border-red-300 px-2.5 text-[11px] font-semibold text-red-700 transition-colors hover:bg-red-100 dark:border-[#4d1111] dark:text-red-300 dark:hover:bg-[#2a1212]"
			onclick={onretry}
		>
			{m.ui_errorstate_try_again()}
		</button>
	{/if}
</div>
