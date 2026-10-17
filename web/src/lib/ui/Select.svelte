<script lang="ts">
	let {
		value = $bindable(''),
		label,
		id,
		name,
		options,
		placeholder,
		disabled = false,
		error,
		class: className = '',
		onchange
	}: {
		value?: string;
		label?: string;
		id?: string;
		name?: string;
		options: { value: string; label: string }[];
		placeholder?: string;
		disabled?: boolean;
		error?: string;
		class?: string;
		onchange?: (event: Event) => void;
	} = $props();

	const uid = $props.id();
	const inputId = $derived(id ?? `select-${uid}`);
	const errorId = $derived(`${inputId}-error`);

	const base =
		'w-full cursor-pointer rounded-md border bg-bg-panel px-2.5 text-xs text-text-main outline-none transition-colors duration-150 focus:border-primary disabled:cursor-not-allowed disabled:opacity-60';
</script>

<div class="flex flex-col gap-1">
	{#if label}
		<label for={inputId} class="text-[11px] font-semibold text-text-muted">{label}</label>
	{/if}
	<select
		id={inputId}
		{name}
		{disabled}
		bind:value
		{onchange}
		class="{base} {error ? 'border-danger' : 'border-border'} h-9 {className}"
		aria-invalid={error ? 'true' : undefined}
		aria-describedby={error ? errorId : undefined}
	>
		{#if placeholder}
			<option value="">{placeholder}</option>
		{/if}
		{#each options as option (option.value)}
			<option value={option.value}>{option.label}</option>
		{/each}
	</select>
	{#if error}
		<p id={errorId} class="text-[11px] text-danger">{error}</p>
	{/if}
</div>
