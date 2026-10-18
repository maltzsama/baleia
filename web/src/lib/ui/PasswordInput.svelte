<script lang="ts">
	import type { FullAutoFill } from 'svelte/elements';

	let {
		value = $bindable(''),
		label,
		id,
		name,
		placeholder,
		autocomplete,
		required = false,
		disabled = false,
		error,
		class: className = '',
		oninput,
		onkeydown
	}: {
		value?: string;
		label?: string;
		id?: string;
		name?: string;
		placeholder?: string;
		autocomplete?: FullAutoFill;
		required?: boolean;
		disabled?: boolean;
		error?: string;
		class?: string;
		oninput?: (event: Event) => void;
		onkeydown?: (event: KeyboardEvent) => void;
	} = $props();

	let revealed = $state(false);
	let inputEl: HTMLInputElement | undefined = $state();
	const uid = $props.id();
	const inputId = $derived(id ?? `password-${uid}`);
	const errorId = $derived(`${inputId}-error`);

	const base =
		'w-full rounded-md border bg-bg-toolbar px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 placeholder:text-text-muted focus:border-primary focus:bg-bg-panel focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] disabled:cursor-not-allowed disabled:opacity-60';

	// The toggle is a real button and never submits the form. Focus returns to
	// the field so typing continues where the user left off (FD11).
	function toggle() {
		revealed = !revealed;
		inputEl?.focus();
	}
</script>

<div class="flex flex-col gap-1">
	{#if label}
		<label for={inputId} class="text-[11px] font-semibold text-text-muted">{label}</label>
	{/if}
	<div class="relative">
		<input
			bind:this={inputEl}
			id={inputId}
			{name}
			type={revealed ? 'text' : 'password'}
			{placeholder}
			{autocomplete}
			{required}
			{disabled}
			bind:value
			{oninput}
			{onkeydown}
			class="{base} {error ? 'border-danger' : 'border-border'} h-9 pr-9 {className}"
			aria-invalid={error ? 'true' : undefined}
			aria-describedby={error ? errorId : undefined}
		/>
		<button
			type="button"
			onclick={toggle}
			aria-pressed={revealed}
			aria-label={revealed ? 'Hide password' : 'Show password'}
			title={revealed ? 'Hide password' : 'Show password'}
			class="absolute inset-y-0 right-0 flex w-9 items-center justify-center rounded-r-md text-text-muted transition-colors hover:text-text-main focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
		>
			{#if revealed}
				<svg
					viewBox="0 0 24 24"
					class="h-4 w-4"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					aria-hidden="true"
				>
					<path
						d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"
					/>
					<line x1="1" y1="1" x2="23" y2="23" />
				</svg>
			{:else}
				<svg
					viewBox="0 0 24 24"
					class="h-4 w-4"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					aria-hidden="true"
				>
					<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
					<circle cx="12" cy="12" r="3" />
				</svg>
			{/if}
		</button>
	</div>
	{#if error}
		<p id={errorId} class="text-[11px] text-danger">{error}</p>
	{/if}
</div>
