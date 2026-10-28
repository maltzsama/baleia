<script lang="ts">
	import type { FullAutoFill } from 'svelte/elements';

	let {
		value = $bindable(''),
		label,
		ariaLabel,
		ariaLabelledby,
		id,
		name,
		type = 'text',
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
		/** Names a field that has no visible label (a cell in a table). */
		ariaLabel?: string;
		/** Names the field by another element, such as a heading. */
		ariaLabelledby?: string;
		id?: string;
		name?: string;
		type?: string;
		placeholder?: string;
		autocomplete?: FullAutoFill;
		required?: boolean;
		disabled?: boolean;
		error?: string;
		class?: string;
		oninput?: (event: Event) => void;
		onkeydown?: (event: KeyboardEvent) => void;
	} = $props();

	const uid = $props.id();
	const inputId = $derived(id ?? `input-${uid}`);
	const errorId = $derived(`${inputId}-error`);

	const base =
		'w-full rounded-md border bg-bg-toolbar px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 placeholder:text-text-muted focus:border-primary focus:bg-bg-panel focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] disabled:cursor-not-allowed disabled:opacity-60';
</script>

<div class="flex flex-col gap-1">
	{#if label}
		<label for={inputId} class="text-[11px] font-semibold text-text-muted">{label}</label>
	{/if}
	<input
		id={inputId}
		{name}
		{type}
		{placeholder}
		{autocomplete}
		{required}
		{disabled}
		bind:value
		{oninput}
		{onkeydown}
		class="{base} {error ? 'border-danger' : 'border-border'} h-9 {className}"
		aria-label={ariaLabel}
		aria-labelledby={ariaLabelledby}
		aria-invalid={error ? 'true' : undefined}
		aria-describedby={error ? errorId : undefined}
	/>
	{#if error}
		<p id={errorId} class="text-[11px] text-danger">{error}</p>
	{/if}
</div>
