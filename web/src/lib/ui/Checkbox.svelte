<script lang="ts">
	let {
		checked = $bindable(false),
		label,
		id,
		name,
		disabled = false,
		class: className = ''
	}: {
		checked?: boolean;
		label: string;
		id?: string;
		name?: string;
		disabled?: boolean;
		class?: string;
	} = $props();

	const uid = $props.id();
	const inputId = $derived(id ?? `checkbox-${uid}`);
</script>

<label
	for={inputId}
	class="inline-flex cursor-pointer items-center gap-2 text-xs text-text-main {disabled
		? 'cursor-not-allowed opacity-60'
		: ''} {className}"
>
	<input
		id={inputId}
		{name}
		{disabled}
		type="checkbox"
		bind:checked
		class="h-3.5 w-3.5 cursor-pointer rounded border-border accent-primary disabled:cursor-not-allowed"
	/>
	{label}
</label>
