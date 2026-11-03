<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import {
		ConnectionRequestError,
		testStored,
		updateConnection,
		type CatalogConnection,
		type TestResult
	} from './api';
	import ProviderForm from './ProviderForm.svelte';
	import { missingFields, type ConnectionProvider } from './providers';

	let {
		open = $bindable(false),
		connection,
		providers,
		onsaved
	}: {
		open?: boolean;
		connection: CatalogConnection;
		providers: ConnectionProvider[];
		onsaved: (connection: CatalogConnection) => void;
	} = $props();

	const provider = $derived(providers.find((item) => item.kind === connection.type) ?? null);
	const canBind = $derived(provider?.family === 'database' || provider?.family === 'metastore');

	let name = $state('');
	let values = $state<Record<string, string>>({});
	let secrets = $state<Record<string, string>>({});
	let saving = $state(false);
	let testing = $state(false);
	let error = $state('');
	let testResult = $state<TestResult | null>(null);

	$effect(() => {
		if (!open) return;
		name = connection.name;
		values = { ...connection.config };
		secrets = {};
		error = '';
		testResult = null;
	});

	const missing = $derived(
		provider ? missingFields(provider, values, secrets, true, connection.has_credentials) : []
	);

	async function save() {
		if (!provider) return;
		saving = true;
		error = '';
		try {
			const updated = await updateConnection(connection.id, {
				name,
				config: values,
				credentials: secrets
			});
			open = false;
			onsaved(updated);
		} catch (e) {
			error =
				e instanceof ConnectionRequestError
					? e.message
					: e instanceof Error
						? e.message
						: String(e);
		} finally {
			saving = false;
		}
	}

	async function test() {
		testing = true;
		testResult = null;
		try {
			testResult = await testStored(connection.id);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			testing = false;
		}
	}
</script>

<Modal bind:open title={m.connections_editconnectiondialog_edit({ name: connection.name })}>
	<div class="flex flex-col gap-3">
		<Input label={m.connections_editconnectiondialog_connection_name()} bind:value={name} />
		{#if provider}
			<ProviderForm
				{provider}
				bind:values
				bind:secrets
				editing
				hasStoredSecrets={connection.has_credentials}
				allowReferences={canBind}
			/>
		{:else}
			<p class="text-[12px] text-danger">
				{m.connections_editconnectiondialog_no_provider_is_registered_for({
					type: connection.type
				})}
			</p>
		{/if}
		{#if error}
			<ErrorPanel error={{ code: 'validation_error', message: error }} />
		{/if}
		{#if testResult}
			<p class="text-[11px] {testResult.success ? 'text-success' : 'text-danger'}">
				{testResult.success
					? m.connections_editconnectiondialog_stored_settings_are_reachable_ms({
							latency_ms: testResult.latency_ms
						})
					: (testResult.error_message ??
						m.connections_editconnectiondialog_the_stored_settings_failed_the_test())}
			</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex items-center justify-between gap-2">
			<Button size="sm" loading={testing} disabled={testing} onclick={test}
				>{m.connections_editconnectiondialog_test_saved_settings()}</Button
			>
			<div class="flex gap-2">
				<Button onclick={() => (open = false)} disabled={saving}>{m.common_cancel()}</Button>
				<Button
					variant="primary"
					loading={saving}
					disabled={saving || missing.length > 0 || !name.trim()}
					onclick={save}
				>
					{m.common_save()}
				</Button>
			</div>
		</div>
	{/snippet}
</Modal>
