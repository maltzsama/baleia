<script lang="ts">
	let {
		value = $bindable(''),
		label,
		ariaLabel,
		ariaLabelledby,
		id,
		name,
		placeholder,
		rows = 4,
		required = false,
		disabled = false,
		error,
		class: className = ''
	}: {
		value?: string;
		label?: string;
		/** Names a field that has no visible label (a cell in a table). */
		ariaLabel?: string;
		/** Names the field by another element, such as a heading. */
		ariaLabelledby?: string;
		id?: string;
		name?: string;
		placeholder?: string;
		rows?: number;
		required?: boolean;
		disabled?: boolean;
		error?: string;
		class?: string;
	} = $props();

	const uid = $props.id();
	const inputId = $derived(id ?? `textarea-${uid}`);
	const errorId = $derived(`${inputId}-error`);

	const base =
		'w-full resize-y rounded-md border bg-bg-toolbar px-2.5 py-2 text-xs leading-[1.5] text-text-main outline-none transition-shadow duration-150 placeholder:text-text-muted focus:border-primary focus:bg-bg-panel focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] disabled:cursor-not-allowed disabled:opacity-60';
</script>

<div class="flex flex-col gap-1">
	{#if label}
		<label for={inputId} class="text-[11px] font-semibold text-text-muted">{label}</label>
	{/if}
	<textarea
		id={inputId}
		{name}
		{placeholder}
		{rows}
		{required}
		{disabled}
		bind:value
		class="{base} {error ? 'border-danger' : 'border-border'} {className}"
		aria-label={ariaLabel}
		aria-labelledby={ariaLabelledby}
		aria-invalid={error ? 'true' : undefined}
		aria-describedby={error ? errorId : undefined}></textarea>
	{#if error}
		<p id={errorId} class="text-[11px] text-danger">{error}</p>
	{/if}
</div>
