<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { resolve } from '$app/paths';
	import { goto } from '$app/navigation';
	import MapPinOffIcon from '@lucide/svelte/icons/map-pin-off';
	import type { Snippet } from 'svelte';
	import { canGoBack } from '$lib/navigation/history';
	import Button from '../Button.svelte';

	// Shared not-found surface (#1062): route-level errors mount it through
	// `+error.svelte`, and resource lookups can reuse it for an in-page 404.
	// The title and description stay optional so the copy is read reactively
	// from the catalog (a language switch re-renders them).
	let {
		title,
		description,
		actions
	}: {
		title?: string;
		description?: string;
		/** Replaces the default recovery pair (Workbench + back). */
		actions?: Snippet;
	} = $props();

	function goBack() {
		// Navigate back only when the app recorded a previous in-app entry;
		// otherwise go to the Workbench instead of leaving the button inert
		// (#1082).
		if (typeof window !== 'undefined' && canGoBack()) window.history.back();
		else void goto(resolve('/sql'));
	}
</script>

<div class="flex w-full flex-col items-center text-center">
	<div
		class="mb-5 flex h-14 w-14 items-center justify-center rounded-full border border-border bg-bg-toolbar text-text-muted"
	>
		<MapPinOffIcon class="h-7 w-7" aria-hidden="true" />
	</div>
	<h1 class="text-[22px] font-semibold tracking-[-.02em] text-text-main">
		{title ?? m.not_found_title()}
	</h1>
	<p class="mt-2 max-w-md text-[13px] leading-[1.55] text-text-muted">
		{description ?? m.not_found_description()}
	</p>
	<div class="mt-6 flex flex-wrap items-center justify-center gap-2">
		{#if actions}
			{@render actions()}
		{:else}
			<a
				href={resolve('/sql')}
				class="inline-flex h-9 items-center justify-center gap-1.5 rounded-md border border-transparent bg-primary-solid px-4 text-[13px] font-semibold text-white transition-colors duration-150 hover:bg-primary-solid-hover"
			>
				{m.not_found_go_to_workbench()}
			</a>
			<Button variant="secondary" onclick={goBack}>{m.not_found_go_back()}</Button>
		{/if}
	</div>
</div>
