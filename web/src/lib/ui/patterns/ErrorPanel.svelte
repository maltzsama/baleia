<script lang="ts">
	import { untrack } from 'svelte';
	import { m } from '$lib/paraglide/messages';
	import { describeError } from '$lib/errors/catalog';
	import CopyButton from './CopyButton.svelte';

	// `open` starts with the details shown: a SQL error is only actionable
	// with the engine's own message.
	let { error, open = false }: { error: unknown; open?: boolean } = $props();

	const described = $derived(describeError(error));
	let details = $state(untrack(() => open));
</script>

<div
	role="alert"
	class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-red-800 dark:border-[#4d1111] dark:bg-[#1f0f0f] dark:text-red-300"
>
	<div class="text-[13px] font-semibold">{described.title}</div>
	<p class="mt-1 text-[12px] leading-[1.5]">{described.explanation}</p>
	{#if described.remediation}
		<p class="mt-1 text-[12px] leading-[1.5]">{described.remediation}</p>
	{/if}
	<button
		type="button"
		class="mt-2 text-[11px] font-semibold underline"
		onclick={() => (details = !details)}
		aria-expanded={details}
	>
		{details ? m.errorpanel_hide_details() : m.errorpanel_details()}
	</button>
	{#if details}
		<dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 font-mono text-[11px]">
			<dt class="opacity-70">{m.errorpanel_code()}</dt>
			<dd>{described.code}</dd>
			{#if described.native}
				<dt class="opacity-70">{m.errorpanel_native()}</dt>
				<dd class="break-all">{described.native}</dd>
			{/if}
			{#if described.requestId}
				<dt class="opacity-70">{m.errorpanel_request()}</dt>
				<dd class="flex items-center gap-1 break-all">
					{described.requestId}
					<CopyButton value={described.requestId} label={m.common_request_id()} />
				</dd>
			{/if}
		</dl>
	{/if}
</div>
