<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import type { ConnectionProvider } from './providers';

	let {
		provider,
		values = $bindable<Record<string, string>>({}),
		secrets = $bindable<Record<string, string>>({}),
		editing = false,
		hasStoredSecrets = false,
		allowReferences = false,
		disabled = false,
		errors = {}
	}: {
		provider: ConnectionProvider;
		values?: Record<string, string>;
		secrets?: Record<string, string>;
		editing?: boolean;
		hasStoredSecrets?: boolean;
		allowReferences?: boolean;
		disabled?: boolean;
		errors?: Record<string, string>;
	} = $props();

	let references = $state<Record<string, boolean>>({});

	function setValue(key: string, value: string) {
		values = { ...values, [key]: value };
	}

	function setSecret(key: string, value: string) {
		secrets = { ...secrets, [key]: value };
	}

	function options(field: { options?: string[] }) {
		return (field.options ?? []).map((option) => ({ value: option, label: option }));
	}

	const secretHint = $derived(
		editing && hasStoredSecrets ? 'Leave blank to keep the stored value.' : undefined
	);
</script>

<div class="flex flex-col gap-3" data-testid="provider-form">
	{#each provider.config_schema as field (field.key)}
		{@const value = values[field.key] ?? ''}
		<div>
			{#if field.type === 'bool'}
				<Checkbox
					label={field.label}
					checked={value === 'true'}
					{disabled}
					onchange={(e) =>
						setValue(field.key, (e.target as HTMLInputElement).checked ? 'true' : 'false')}
				/>
			{:else if field.type === 'select'}
				<Select
					label={field.label + (field.required ? ' *' : '')}
					value={value || (field.default ?? '')}
					options={options(field)}
					placeholder={field.required
						? m.connections_providerform_choose()
						: m.connections_providerform_default()}
					{disabled}
					error={errors[field.key]}
					onchange={(e) => setValue(field.key, (e.target as HTMLSelectElement).value)}
				/>
			{:else if field.secret}
				<Input
					label={field.label + (field.required ? ' *' : '')}
					type={references[field.key] ? 'text' : 'password'}
					value={secrets[field.key] ?? ''}
					placeholder={references[field.key]
						? '@baleia-secret[env:NAME]'
						: field.required
							? m.connections_providerform_required()
							: m.common_optional()}
					autocomplete="new-password"
					{disabled}
					error={errors[field.key]}
					oninput={(e) => setSecret(field.key, (e.target as HTMLInputElement).value)}
					class={references[field.key] ? 'font-mono' : ''}
				/>
				{#if allowReferences}
					<div class="mt-1 flex items-center justify-between gap-2">
						<Checkbox
							label="Use an env/file reference"
							checked={references[field.key] ?? false}
							{disabled}
							onchange={(e) => (references[field.key] = (e.target as HTMLInputElement).checked)}
						/>
						{#if secretHint}
							<span class="text-[10px] text-text-muted">{secretHint}</span>
						{/if}
					</div>
				{:else if secretHint}
					<p class="mt-1 text-[10px] text-text-muted">{secretHint}</p>
				{/if}
			{:else}
				<Input
					label={field.label + (field.required ? ' *' : '')}
					type={field.type === 'int' ? 'number' : 'text'}
					{value}
					placeholder={field.default ??
						(field.required ? m.connections_providerform_required() : m.common_optional())}
					autocomplete="off"
					{disabled}
					error={errors[field.key]}
					oninput={(e) => setValue(field.key, (e.target as HTMLInputElement).value)}
					class={field.type === 'url' || field.type === 'file_path' ? 'font-mono' : ''}
				/>
			{/if}
			{#if field.help}
				<p class="mt-1 text-[10px] leading-relaxed text-text-muted">{field.help}</p>
			{/if}
		</div>
	{/each}
</div>
