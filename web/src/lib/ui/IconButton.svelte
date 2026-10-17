<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		label,
		type = 'button',
		disabled = false,
		class: className = '',
		onclick,
		children
	}: {
		/** Accessible name; rendered as aria-label and the native tooltip. */
		label: string;
		type?: 'button' | 'submit' | 'reset';
		disabled?: boolean;
		class?: string;
		onclick?: (event: MouseEvent) => void;
		children: Snippet;
	} = $props();
</script>

<button
	{type}
	{disabled}
	aria-label={label}
	title={label}
	class="inline-flex h-7 w-7 items-center justify-center rounded-md border border-transparent text-text-muted transition-colors duration-150 hover:enabled:bg-bg-toolbar hover:enabled:text-text-main disabled:cursor-not-allowed disabled:opacity-50 {className}"
	{onclick}
>
	{@render children()}
</button>
