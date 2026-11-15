<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { m } from '$lib/paraglide/messages';
	import { describeError } from '$lib/errors/catalog';
	import Button from '../Button.svelte';
	import NotFound from './NotFound.svelte';

	// Status-aware error boundary (#1062). A missing route renders the
	// first-class 404 surface; anything else maps through the error catalog so a
	// raw API/engine message never becomes the headline (FD7).
	const described = $derived(
		page.status === 403 ? describeError('forbidden') : describeError('internal')
	);
</script>

<div
	class="flex w-full flex-1 min-h-0 flex-col items-center justify-center overflow-y-auto px-6 py-16 text-center"
>
	{#if page.status === 404}
		<NotFound />
	{:else}
		<h1 class="text-[22px] font-semibold tracking-[-.02em] text-text-main">{described.title}</h1>
		<p class="mt-2 max-w-md text-[13px] leading-[1.55] text-text-muted">{described.explanation}</p>
		{#if described.remediation}
			<p class="mt-1 max-w-md text-[13px] leading-[1.55] text-text-muted">
				{described.remediation}
			</p>
		{/if}
		<div class="mt-6 flex flex-wrap items-center justify-center gap-2">
			<Button variant="primary" onclick={() => window.location.reload()}>
				{m.ui_errorstate_try_again()}
			</Button>
			<a
				href={resolve('/sql')}
				class="inline-flex h-9 items-center justify-center gap-1.5 rounded-md border border-border bg-transparent px-4 text-[13px] font-semibold text-text-muted transition-colors duration-150 hover:border-border-hover hover:text-text-main"
			>
				{m.not_found_go_to_workbench()}
			</a>
		</div>
	{/if}
</div>
