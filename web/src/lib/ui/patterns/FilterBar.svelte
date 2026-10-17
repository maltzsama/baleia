<script lang="ts">
	import Button from '../Button.svelte';
	import Input from '../Input.svelte';
	import Select from '../Select.svelte';
	import Checkbox from '../Checkbox.svelte';

	type Field =
		| { key: string; label: string; type: 'text' }
		| { key: string; label: string; type: 'select'; options: { value: string; label: string }[] }
		| {
				key: string;
				label: string;
				type: 'multiselect';
				options: { value: string; label: string }[];
		  }
		| { key: string; label: string; type: 'boolean' }
		| { key: string; label: string; type: 'daterange' };

	type Value = Record<string, string | string[] | boolean>;

	let {
		fields,
		value = $bindable({}),
		onchange
	}: {
		fields: Field[];
		value?: Value;
		onchange?: (value: Value) => void;
	} = $props();

	let timer: ReturnType<typeof setTimeout> | undefined;

	function emit(next: Value, debounce = false) {
		value = next;
		if (timer) clearTimeout(timer);
		if (debounce) {
			timer = setTimeout(() => onchange?.(value), 250);
		} else {
			onchange?.(value);
		}
	}

	function textValue(key: string): string {
		const raw = value[key];
		return typeof raw === 'string' ? raw : '';
	}

	function selectValue(key: string): string {
		const raw = value[key];
		return typeof raw === 'string' ? raw : '';
	}

	function boolValue(key: string): boolean {
		return value[key] === true;
	}

	function listValue(key: string): string[] {
		const raw = value[key];
		return Array.isArray(raw) ? raw : [];
	}

	function toggleList(key: string, option: string) {
		const current = listValue(key);
		const next = current.includes(option)
			? current.filter((item) => item !== option)
			: [...current, option];
		emit({ ...value, [key]: next });
	}

	function clearAll() {
		emit({});
	}

	const activeCount = $derived(
		Object.values(value).filter((item) =>
			Array.isArray(item) ? item.length > 0 : item !== '' && item !== false
		).length
	);
</script>

<div class="flex flex-wrap items-end gap-2 rounded-lg border border-border bg-bg-panel p-3">
	{#each fields as field (field.key)}
		{#if field.type === 'text'}
			<div class="w-48">
				<Input
					label={field.label}
					value={textValue(field.key)}
					oninput={(event) =>
						emit({ ...value, [field.key]: (event.currentTarget as HTMLInputElement).value }, true)}
				/>
			</div>
		{:else if field.type === 'select'}
			<div class="w-44">
				<Select
					label={field.label}
					value={selectValue(field.key)}
					options={field.options}
					placeholder="Any"
					onchange={(event) =>
						emit({ ...value, [field.key]: (event.currentTarget as HTMLSelectElement).value })}
				/>
			</div>
		{:else if field.type === 'multiselect'}
			<div class="flex flex-col gap-1">
				<span class="text-[11px] font-semibold text-text-muted">{field.label}</span>
				<div class="flex flex-wrap gap-1">
					{#each field.options as option (option.value)}
						<button
							type="button"
							class="rounded-full border px-2 py-0.5 text-[11px] font-semibold transition-colors {listValue(
								field.key
							).includes(option.value)
								? 'border-primary bg-primary text-white'
								: 'border-border text-text-muted hover:border-border-hover'}"
							onclick={() => toggleList(field.key, option.value)}
						>
							{option.label}
						</button>
					{/each}
				</div>
			</div>
		{:else if field.type === 'boolean'}
			<Checkbox
				label={field.label}
				checked={boolValue(field.key)}
				onchange={(event) =>
					emit({ ...value, [field.key]: (event.currentTarget as HTMLInputElement).checked })}
			/>
		{:else if field.type === 'daterange'}
			<div class="flex items-end gap-1">
				<div class="w-36">
					<Input
						label={`${field.label} from`}
						type="date"
						value={textValue(`${field.key}_from`)}
						oninput={(event) =>
							emit(
								{
									...value,
									[`${field.key}_from`]: (event.currentTarget as HTMLInputElement).value
								},
								true
							)}
					/>
				</div>
				<div class="w-36">
					<Input
						label="to"
						type="date"
						value={textValue(`${field.key}_to`)}
						oninput={(event) =>
							emit(
								{
									...value,
									[`${field.key}_to`]: (event.currentTarget as HTMLInputElement).value
								},
								true
							)}
					/>
				</div>
			</div>
		{/if}
	{/each}

	<div class="ml-auto flex items-center gap-2">
		{#if activeCount > 0}
			<span class="text-[11px] text-text-muted">{activeCount} active</span>
			<Button size="sm" onclick={clearAll}>Clear all</Button>
		{/if}
	</div>
</div>
