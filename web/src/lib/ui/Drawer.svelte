<script lang="ts">
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

	let panelEl: HTMLDivElement | undefined = $state();

	$effect(() => {
		if (open) panelEl?.focus();
	});

	function close() {
		open = false;
		onclose?.();
	}

	function onkeydown(event: KeyboardEvent) {
		if (open && event.key === 'Escape') close();
	}
</script>

<svelte:window {onkeydown} />

{#if open}
	<div
		class="fixed inset-0 z-[100] flex justify-end bg-black/30 backdrop-blur-[2px]"
		role="presentation"
		onclick={(event) => event.target === event.currentTarget && close()}
	>
		<div
			bind:this={panelEl}
			tabindex="-1"
			role="dialog"
			aria-modal="true"
			aria-label={title}
			class="flex h-full w-full max-w-[420px] flex-col border-l border-border bg-bg-panel text-text-main shadow-[0_0_48px_rgba(0,0,0,.18)] outline-none"
		>
			<header class="flex items-center border-b border-border px-5 py-[18px]">
				<h2 class="flex-1 text-sm font-semibold">{title}</h2>
				<button
					type="button"
					class="flex h-[26px] w-[26px] items-center justify-center rounded-md text-text-muted hover:bg-bg-toolbar hover:text-text-main"
					onclick={close}
					aria-label="Close"
				>
					×
				</button>
			</header>
			<div class="flex-1 overflow-y-auto p-5">{@render children()}</div>
			{#if footer}
				<footer class="border-t border-border px-5 py-3.5">{@render footer()}</footer>
			{/if}
		</div>
	</div>
{/if}
