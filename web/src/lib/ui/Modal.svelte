<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { Snippet } from 'svelte';

	let {
		open = $bindable(false),
		title,
		onclose,
		children,
		footer
	}: {
		open?: boolean;
		title: string;
		onclose?: () => void;
		children: Snippet;
		footer?: Snippet;
	} = $props();

	let dialogEl: HTMLDialogElement | undefined = $state();
	let previouslyFocused: HTMLElement | null = null;

	// The native <dialog> owns the focus trap and Esc handling; this effect only
	// mirrors the `open` prop onto showModal()/close().
	$effect(() => {
		if (!dialogEl) return;
		if (open && !dialogEl.open) {
			previouslyFocused = document.activeElement as HTMLElement | null;
			dialogEl.showModal();
		} else if (!open && dialogEl.open) {
			dialogEl.close();
		}
	});

	function close() {
		dialogEl?.close();
	}

	function handleClose() {
		open = false;
		previouslyFocused?.focus();
		previouslyFocused = null;
		onclose?.();
	}
</script>

<dialog
	bind:this={dialogEl}
	onclose={handleClose}
	class="m-auto w-full max-w-[480px] rounded-xl border border-border bg-bg-panel p-0 text-text-main shadow-[0_24px_64px_rgba(0,0,0,.2)] backdrop:bg-black/40"
>
	{#if open}
		<div class="flex max-h-[85vh] flex-col">
			<header class="flex items-center border-b border-border px-5 py-[18px]">
				<h2 class="flex-1 text-sm font-semibold">{title}</h2>
				<button
					type="button"
					class="flex h-[26px] w-[26px] items-center justify-center rounded-md text-text-muted hover:bg-bg-toolbar hover:text-text-main"
					onclick={close}
					aria-label={m.common_close()}
				>
					×
				</button>
			</header>
			<div class="overflow-y-auto p-5">{@render children()}</div>
			{#if footer}
				<footer class="border-t border-border px-5 py-3.5">{@render footer()}</footer>
			{/if}
		</div>
	{/if}
</dialog>
