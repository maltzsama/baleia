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
	let panelEl: HTMLDivElement | undefined = $state();
	let previouslyFocused: HTMLElement | null = null;

	// A native modal <dialog>, like Modal (#483): it keeps Tab inside, makes
	// the page behind inert and closes on Esc. This effect mirrors `open` onto
	// showModal()/close() and starts focus on the panel.
	$effect(() => {
		if (!dialogEl) return;
		if (open && !dialogEl.open) {
			previouslyFocused = document.activeElement as HTMLElement | null;
			dialogEl.showModal();
			panelEl?.focus();
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

<!-- A click on the backdrop lands on the dialog itself, outside the panel. -->
<dialog
	bind:this={dialogEl}
	onclose={handleClose}
	onclick={(event) => event.target === dialogEl && close()}
	aria-label={title}
	class="fixed inset-y-0 right-0 left-auto m-0 h-dvh max-h-none w-full max-w-[420px] border-0 border-l border-border bg-bg-panel p-0 text-text-main shadow-[0_0_48px_rgba(0,0,0,.18)] backdrop:bg-black/30 backdrop:backdrop-blur-[2px]"
>
	{#if open}
		<div bind:this={panelEl} tabindex="-1" class="flex h-full flex-col outline-none">
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
			<div class="flex-1 overflow-y-auto p-5">{@render children()}</div>
			{#if footer}
				<footer class="border-t border-border px-5 py-3.5">{@render footer()}</footer>
			{/if}
		</div>
	{/if}
</dialog>
