<script lang="ts">
	import type { Snippet } from 'svelte';

	type Variant = 'primary' | 'secondary' | 'danger' | 'ghost';
	type Size = 'sm' | 'md';

	let {
		variant = 'secondary',
		size = 'md',
		type = 'button',
		disabled = false,
		loading = false,
		class: className = '',
		onclick,
		children
	}: {
		variant?: Variant;
		size?: Size;
		type?: 'button' | 'submit' | 'reset';
		disabled?: boolean;
		loading?: boolean;
		class?: string;
		onclick?: (event: MouseEvent) => void;
		children: Snippet;
	} = $props();

	const base =
		'inline-flex items-center justify-center gap-1.5 rounded-md border font-semibold transition-colors duration-150 disabled:opacity-50 disabled:cursor-not-allowed';
	const sizes: Record<Size, string> = {
		sm: 'h-7 px-2.5 text-[11px]',
		md: 'h-9 px-4 text-[13px]'
	};
	const variants: Record<Variant, string> = {
		primary: 'bg-primary text-white border-transparent hover:enabled:bg-primary-hover',
		secondary:
			'bg-transparent text-text-muted border-border hover:enabled:border-border-hover hover:enabled:text-text-main',
		danger: 'bg-danger text-white border-transparent hover:enabled:bg-red-700',
		ghost:
			'bg-transparent text-text-muted border-transparent hover:enabled:bg-bg-toolbar hover:enabled:text-text-main'
	};
</script>

<button
	{type}
	disabled={disabled || loading}
	class="{base} {sizes[size]} {variants[variant]} {className}"
	aria-busy={loading}
	{onclick}
>
	{#if loading}
		<span
			class="h-3 w-3 animate-spin rounded-full border-2 border-current border-t-transparent"
			aria-hidden="true"
		></span>
	{/if}
	{@render children()}
</button>
