<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { Snippet } from 'svelte';

	let {
		open = $bindable(false),
		title,
		size = 'md',
		flush = false,
		onclose,
		children,
		footer
	}: {
		open?: boolean;
		title: string;
		/** md for forms; lg and xl for content such as previews and results. */
		size?: 'md' | 'lg' | 'xl';
		/** The body fills a fixed height with no padding (a results panel). */
		flush?: boolean;
		onclose?: () => void;
		children: Snippet;
		footer?: Snippet;
	} = $props();

	// The dialog is named by its title.
	const uid = $props.id();
	const titleId = `${uid}-title`;
	const widths = { md: 'max-w-[480px]', lg: 'max-w-[900px]', xl: 'max-w-[1100px]' };

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
	aria-labelledby={titleId}
	class="m-auto w-full {widths[
		size
	]} rounded-xl border border-border bg-bg-panel p-0 text-text-main shadow-[0_24px_64px_rgba(0,0,0,.2)] backdrop:bg-black/40"
>
	{#if open}
		<div class="flex flex-col {flush ? 'h-[80vh]' : 'max-h-[85vh]'}">
			<header class="flex items-center border-b border-border px-5 py-[18px]">
				<h2 id={titleId} class="flex-1 text-sm font-semibold">{title}</h2>
				<button
					type="button"
					class="flex h-[26px] w-[26px] items-center justify-center rounded-md text-text-muted hover:bg-bg-toolbar hover:text-text-main"
					onclick={close}
					aria-label={m.common_close()}
				>
					×
				</button>
			</header>
			<div class={flush ? 'flex min-h-0 flex-1 flex-col' : 'overflow-y-auto p-5'}>
				{@render children()}
			</div>
			{#if footer}
				<footer class="border-t border-border px-5 py-3.5">{@render footer()}</footer>
			{/if}
		</div>
	{/if}
</dialog>
