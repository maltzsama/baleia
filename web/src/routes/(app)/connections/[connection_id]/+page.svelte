<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import ActivityPanel from '$lib/audit/ActivityPanel.svelte';
	import {
		createCatalog,
		createEngine,
		deleteConnection,
		getConnection,
		bindingSupport,
		getConnectionImpact,
		listConnectionCatalogs,
		listEngines,
		testStored,
		type BindingSupport,
		type Catalog,
		type CatalogConnection,
		type CatalogConnectionImpact,
		type Engine,
		type TestResult
	} from '$lib/connections/api';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import EngineBadge from '$lib/ui/patterns/EngineBadge.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const connectionId = $derived(page.params.connection_id ?? '');

	let conn = $state<CatalogConnection | null>(null);
	let catalogs = $state<Catalog[]>([]);
	let engines = $state<Engine[]>([]);
	let impact = $state<CatalogConnectionImpact | null>(null);
	let loading = $state(true);
	let error = $state('');

	let testOpen = $state(false);
	let testResult = $state<TestResult | null>(null);
	let testError = $state('');
	let testing = $state(false);

	let registerOpen = $state(false);
	let engineName = $state('');
	let registering = $state(false);
	let registerError = $state('');

	let catalogOpen = $state(false);
	let catalogName = $state('');
	let catalogTrinoName = $state('');
	let catalogWarehouse = $state('');
	let catalogConnector = $state('');
	let support = $state<BindingSupport | null>(null);
	let catalogSaving = $state(false);
	let catalogError = $state('');

	let deleteOpen = $state(false);

	const engine = $derived(engines.find((item) => item.connection_id === connectionId));
	const readOnly = $derived(conn?.credentials_read_only ?? false);

	async function load() {
		if (!connectionId) return;
		loading = true;
		error = '';
		try {
			conn = await getConnection(connectionId);
			[catalogs, engines, impact] = await Promise.all([
				listConnectionCatalogs(connectionId),
				listEngines(),
				getConnectionImpact(connectionId)
			]);
			const federated = engines.find((item) => item.class === 'federated');
			if (federated && conn) {
				const supports = await bindingSupport(federated.id);
				support = supports[conn.type] ?? null;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function runTest() {
		if (!connectionId) return;
		testing = true;
		testError = '';
		testResult = null;
		try {
			testResult = await testStored(connectionId);
			testOpen = true;
		} catch (e) {
			testError = e instanceof Error ? e.message : String(e);
			testOpen = true;
		} finally {
			testing = false;
		}
	}

	function openRegister() {
		registerError = '';
		engineName = (conn?.name ?? '')
			.toLowerCase()
			.replace(/[^a-z0-9_-]+/g, '_')
			.replace(/^[^a-z]+/, '');
		registerOpen = true;
	}

	async function submitRegister() {
		if (!engineName) {
			registerError = 'Name the engine.';
			return;
		}
		registering = true;
		registerError = '';
		try {
			const created = await createEngine({ name: engineName, connection_id: connectionId });
			registerOpen = false;
			window.location.href = `/engines/${created.id}`;
		} catch (e) {
			registerError = e instanceof Error ? e.message : String(e);
		} finally {
			registering = false;
		}
	}

	function openCatalog() {
		catalogError = '';
		catalogName = (conn?.name ?? '').replace(/[^a-zA-Z0-9_]+/g, '_');
		catalogTrinoName = (conn?.name ?? '')
			.toLowerCase()
			.replace(/[^a-z0-9_]+/g, '_')
			.replace(/^[^a-z]+/, 'c_');
		catalogWarehouse = '';
		catalogConnector = support?.connectors[0] ?? '';
		catalogOpen = true;
	}

	async function submitCatalog() {
		if (!catalogName || !catalogTrinoName || !catalogWarehouse) {
			catalogError = 'Name, engine name and warehouse are required.';
			return;
		}
		catalogSaving = true;
		catalogError = '';
		try {
			await createCatalog(connectionId, {
				name: catalogName,
				trino_catalog_name: catalogTrinoName,
				warehouse: catalogWarehouse,
				connector: catalogConnector
			});
			catalogs = await listConnectionCatalogs(connectionId);
			catalogOpen = false;
		} catch (e) {
			catalogError = e instanceof Error ? e.message : String(e);
		} finally {
			catalogSaving = false;
		}
	}

	async function confirmDelete() {
		await deleteConnection(connectionId);
		window.location.href = '/connections';
	}

	function consequences(): string[] {
		if (!impact) return [];
		return [
			impact.engine
				? `Deletes the engine ${impact.engine.name} and its catalogs.`
				: 'No engine backs this connection.',
			`${impact.catalogs} catalog(s) and ${impact.bindings} binding(s) depend on it.`,
			`${impact.running_executions} execution(s) are running now.`
		];
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	{#if loading && !conn}
		<Skeleton class="h-24 w-full" />
	{:else if error && !conn}
		<ErrorState title="Could not load the connection" message={error} onretry={load} />
	{:else if conn}
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div>
				<div class="flex items-center gap-2">
					<h1 class="text-xl font-semibold tracking-[-0.3px]">{conn.name}</h1>
					<Badge tone="neutral">{conn.type}</Badge>
					{#if readOnly}<Badge tone="warning">Read-only credentials</Badge>{/if}
				</div>
				<p class="mt-1 text-[12px] text-slate-600 dark:text-slate-300">
					created <RelativeTime at={conn.created_at} />
				</p>
			</div>
			<div class="flex flex-wrap gap-2">
				<Button loading={testing} disabled={testing} onclick={runTest}>Test</Button>
				{#if !engine && (conn.allowed_actions ?? []).includes('register_engine')}
					<Button onclick={openRegister}>Register as engine</Button>
				{/if}
				<Button variant="danger" onclick={() => (deleteOpen = true)}>Delete</Button>
			</div>
		</div>

		<div class="grid gap-4 lg:grid-cols-2">
			<div class="rounded-lg border border-border bg-bg-panel p-4">
				<h2 class="text-[13px] font-semibold">Configuration</h2>
				<dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
					{#each Object.entries(conn.config) as [key, value] (key)}
						<dt class="font-mono text-slate-600 dark:text-slate-300">{key}</dt>
						<dd class="break-all font-mono">{value}</dd>
					{:else}
						<dt class="text-slate-600 dark:text-slate-300">Config</dt>
						<dd>—</dd>
					{/each}
					<dt class="text-slate-600 dark:text-slate-300">Credentials</dt>
					<dd>{conn.has_credentials ? 'Stored' : 'None'}</dd>
					<dt class="text-slate-600 dark:text-slate-300">Default read-only</dt>
					<dd>{conn.default_readonly ? 'Yes' : 'No'}</dd>
				</dl>
				<p class="mt-3 text-[11px] text-slate-600 dark:text-slate-300">
					{readOnly
						? 'An administrator asserts these credentials cannot modify data: read-only grants and agents may use this engine.'
						: 'Read-only grants and agents need an administrator to assert read-only credentials on engines that cannot enforce read-only sessions.'}
				</p>
			</div>

			<div class="rounded-lg border border-border bg-bg-panel p-4">
				<h2 class="text-[13px] font-semibold">Used as</h2>
				<div class="mt-2 text-[11px]">
					<p class="font-semibold">Direct engine</p>
					{#if engine}
						<EngineBadge {engine} />
					{:else}
						<p class="text-slate-600 dark:text-slate-300">Not registered as an engine.</p>
					{/if}
				</div>
				<div class="mt-4 flex items-center justify-between">
					<p class="text-[11px] font-semibold">Catalog source</p>
					{#if support && support.connectors.length > 0}
						<Button size="sm" onclick={openCatalog}>Add catalog</Button>
					{/if}
				</div>
				{#if catalogs.length === 0}
					<p class="mt-1 text-[11px] text-slate-600 dark:text-slate-300">
						No catalogs from this connection.
					</p>
				{:else}
					<ul class="mt-1 flex flex-col gap-1 text-[11px]">
						{#each catalogs as catalog (catalog.id)}
							<li class="flex items-center justify-between gap-2">
								<button
									type="button"
									class="text-primary hover:underline"
									onclick={() => (window.location.href = `/catalogs/${catalog.id}`)}
								>
									{catalog.name}
								</button>
								<Badge tone="neutral">{catalog.trino_catalog_name ?? ''}</Badge>
							</li>
						{/each}
					</ul>
				{/if}
			</div>
		</div>

		<div class="mt-6">
			<ActivityPanel
				resourceType="connection"
				resourceId={connectionId}
				title="Connection activity"
			/>
		</div>
	{/if}
</div>

<Modal bind:open={testOpen} title="Connection test">
	{#if testError}
		<p class="text-[12px] text-danger" role="alert">{testError}</p>
	{:else if testResult}
		<div class="flex flex-col gap-2 text-[12px]">
			<p class="font-semibold">
				{testResult.success ? 'All checks passed' : 'Checks failed'}
				<span class="ml-1 font-normal text-slate-600 dark:text-slate-300"
					>{testResult.latency_ms} ms</span
				>
			</p>
			{#if testResult.error_message}<p class="text-danger">{testResult.error_message}</p>{/if}
			<ul class="flex flex-col gap-1">
				{#each testResult.checks ?? [] as check (check.name)}
					<li class="flex items-start gap-2">
						<Badge tone={check.skipped ? 'neutral' : check.ok ? 'success' : 'danger'}>
							{check.skipped ? 'Skipped' : check.ok ? 'OK' : 'Failed'}
						</Badge>
						<span class="font-mono">{check.name}</span>
						<span class="text-slate-600 dark:text-slate-300"
							>{check.detail ?? check.error_code ?? ''}</span
						>
					</li>
				{/each}
			</ul>
		</div>
	{/if}
</Modal>

<Modal bind:open={registerOpen} title="Register as engine">
	<div class="flex flex-col gap-3 text-[12px]">
		<Input label="Engine name" bind:value={engineName} class="font-mono" />
		<p class="text-[11px] text-slate-600 dark:text-slate-300">
			The engine probes this connection and discovers its native catalogs.
		</p>
		{#if registerError}<p class="text-danger" role="alert">{registerError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (registerOpen = false)} disabled={registering}>Cancel</Button>
			<Button
				variant="primary"
				loading={registering}
				disabled={registering}
				onclick={submitRegister}
			>
				Register
			</Button>
		</div>
	{/snippet}
</Modal>

<Modal bind:open={catalogOpen} title="Add catalog">
	<div class="flex flex-col gap-3 text-[12px]">
		<Input label="Name" bind:value={catalogName} />
		<Input label="Engine name" bind:value={catalogTrinoName} class="font-mono" />
		<Input label="Warehouse / database" bind:value={catalogWarehouse} class="font-mono" />
		<Select
			label="Connector"
			bind:value={catalogConnector}
			options={(support?.connectors ?? []).map((connector) => ({
				value: connector,
				label: connector
			}))}
		/>
		{#if catalogError}<p class="text-danger" role="alert">{catalogError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (catalogOpen = false)} disabled={catalogSaving}>Cancel</Button>
			<Button
				variant="primary"
				loading={catalogSaving}
				disabled={catalogSaving}
				onclick={submitCatalog}
			>
				Add
			</Button>
		</div>
	{/snippet}
</Modal>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete connection"
	resourceName={conn?.name ?? ''}
	confirmLabel="Delete connection"
	typeToConfirm
	consequences={consequences()}
	onconfirm={confirmDelete}
/>
