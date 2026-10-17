<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import KindIcon from '$lib/ui/patterns/KindIcon.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import {
		bindingSupport,
		createCatalog,
		createEngine,
		deleteConnection,
		listCatalogBindings,
		listConnectionCatalogs,
		listConnections,
		listEngines,
		loadProviders,
		testStored,
		type BindingSupport,
		type Catalog,
		type CatalogBinding,
		type CatalogConnection,
		type ConnectionProvider,
		type Engine
	} from '$lib/connections/api';
	import EditConnectionDialog from '$lib/connections/EditConnectionDialog.svelte';
	import {
		canQueryDirectly,
		normalizeCatalogName,
		normalizeEngineName
	} from '$lib/connections/providers';

	type CatalogInfo = { catalog: Catalog; bindings: CatalogBinding[] };

	let connections = $state<CatalogConnection[]>([]);
	let providers = $state<ConnectionProvider[]>([]);
	let engines = $state<Engine[]>([]);
	let support = $state<Record<string, Record<string, BindingSupport>>>({});
	let catalogs = $state<Record<string, CatalogInfo[]>>({});
	let loading = $state(true);
	let error = $state('');
	let search = $state('');

	let editing = $state<CatalogConnection | null>(null);
	let editOpen = $state(false);
	let deleting = $state<CatalogConnection | null>(null);
	let deleteOpen = $state(false);
	let adding = $state<CatalogConnection | null>(null);
	let addOpen = $state(false);
	let addName = $state('');
	let addConnector = $state('');
	let addingBusy = $state(false);
	let registeringId = $state('');
	let testingId = $state('');

	const filtered = $derived(
		connections.filter((c) => !search || c.name.toLowerCase().includes(search.toLowerCase()))
	);

	function providerOf(type: string): ConnectionProvider | undefined {
		return providers.find((p) => p.kind === type);
	}

	function enginesOf(connectionId: string): Engine[] {
		return engines.filter((e) => e.connection_id === connectionId);
	}

	function compatibleEngines(type: string): Engine[] {
		return engines.filter((e) => Boolean(support[e.id]?.[type]));
	}

	function connectorsFor(connection: CatalogConnection): string[] {
		const engine = compatibleEngines(connection.type)[0];
		return engine ? (support[engine.id]?.[connection.type]?.connectors ?? []) : [];
	}

	function summary(connection: CatalogConnection): string {
		const config = connection.config ?? {};
		if (config.host) {
			return [config.host, config.port, config.database].filter(Boolean).join('/');
		}
		return config.url ?? connection.url ?? '';
	}

	function syncStatus(status: string | undefined): 'ok' | 'running' | 'failed' | 'stale' {
		switch (status) {
			case 'finished':
				return 'ok';
			case 'running':
				return 'running';
			case 'failed':
				return 'failed';
			default:
				return 'stale';
		}
	}

	async function load() {
		loading = true;
		error = '';
		try {
			const [connectionList, providerList, engineList] = await Promise.all([
				listConnections(),
				loadProviders(),
				listEngines()
			]);
			connections = connectionList;
			providers = providerList;
			engines = engineList;
			const supports = await Promise.all(
				engineList.map(async (engine) => {
					try {
						return [engine.id, await bindingSupport(engine.id)] as const;
					} catch {
						return [engine.id, {}] as const;
					}
				})
			);
			support = Object.fromEntries(supports);
			await Promise.all(connectionList.map(loadCatalogs));
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function loadCatalogs(connection: CatalogConnection) {
		try {
			const list = await listConnectionCatalogs(connection.id);
			const withBindings = await Promise.all(
				list.map(async (catalog) => ({
					catalog,
					bindings: catalog.id ? await listCatalogBindings(catalog.id).catch(() => []) : []
				}))
			);
			catalogs = { ...catalogs, [connection.id]: withBindings };
		} catch {
			catalogs = { ...catalogs, [connection.id]: [] };
		}
	}

	async function test(connection: CatalogConnection) {
		testingId = connection.id;
		try {
			const result = await testStored(connection.id);
			if (result.success) {
				toasts.success(`${connection.name}: reachable (${result.latency_ms} ms)`);
			} else {
				toasts.error(`${connection.name}: ${result.error_message ?? 'the test failed'}`);
			}
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			testingId = '';
		}
	}

	async function registerEngine(connection: CatalogConnection) {
		registeringId = connection.id;
		try {
			const name = normalizeEngineName(connection.name);
			if (!name) throw new Error('The connection name cannot be used as an engine name.');
			const engine = await createEngine({ name, connection_id: connection.id });
			engines = [...engines, engine];
			toasts.success(`Engine ${engine.name} registered`);
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			registeringId = '';
		}
	}

	function openAdd(connection: CatalogConnection) {
		adding = connection;
		addName = '';
		addConnector = connectorsFor(connection)[0] ?? '';
		addOpen = true;
	}

	async function addCatalog() {
		if (!adding) return;
		const target = adding;
		addingBusy = true;
		try {
			const name = normalizeCatalogName(addName);
			if (!name) throw new Error('Choose a name that can be used as a SQL identifier.');
			await createCatalog(target.id, {
				name: addName,
				trino_catalog_name: name,
				warehouse: addName,
				connector: addConnector,
				description: ''
			});
			toasts.success(`Catalog ${addName} created`);
			addOpen = false;
			adding = null;
			await load();
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			addingBusy = false;
		}
	}

	function onSaved(updated: CatalogConnection) {
		connections = connections.map((c) => (c.id === updated.id ? updated : c));
		toasts.success('Connection saved');
	}

	function consequences(connection: CatalogConnection): string[] {
		const items: string[] = [];
		const ownEngines = enginesOf(connection.id);
		if (ownEngines.length > 0) {
			items.push(`Its engine ${ownEngines.map((e) => e.name).join(', ')} must be deleted first.`);
		}
		const catalogsHere = catalogs[connection.id] ?? [];
		if (catalogsHere.length > 0) {
			items.push(
				`${catalogsHere.length} catalog(s) and their bindings will be removed: ${catalogsHere
					.map((item) => item.catalog.name)
					.join(', ')}.`
			);
		}
		items.push('Saved queries and metrics that target these catalogs will stop resolving.');
		return items;
	}

	async function remove() {
		if (!deleting) return;
		const target = deleting;
		await deleteConnection(target.id);
		connections = connections.filter((c) => c.id !== target.id);
		deleting = null;
		toasts.success('Connection removed');
	}

	onMount(() => {
		void load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-5 flex items-end justify-between">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">Connections</h1>
			<p class="mt-1 text-[13px] text-text-muted">
				Providers, engines, catalogs and the engines each catalog is bound to.
			</p>
		</div>
		<Button variant="primary" onclick={() => (window.location.href = '/connections/new')}>
			New connection
		</Button>
	</div>

	<div class="mb-5 flex items-center gap-2.5">
		<div class="w-64">
			<Input bind:value={search} placeholder="Search connections…" />
		</div>
		<span class="ml-auto font-mono text-xs text-text-muted"
			>{filtered.length} connection{filtered.length === 1 ? '' : 's'}</span
		>
	</div>

	{#if loading}
		<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" aria-label="Loading connections">
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-40 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title="Could not load connections" message={error} onretry={load} />
	{:else if filtered.length === 0}
		<EmptyState
			title="No connections"
			description="Connect a catalog manager, a metastore or a database to start querying."
		>
			<Button variant="primary" onclick={() => (window.location.href = '/connections/new')}>
				New connection
			</Button>
		</EmptyState>
	{:else}
		<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
			{#each filtered as connection (connection.id)}
				{@const provider = providerOf(connection.type)}
				{@const ownEngines = enginesOf(connection.id)}
				{@const catalogsHere = catalogs[connection.id] ?? []}
				<article class="flex flex-col gap-3 rounded-lg border border-border bg-bg-panel p-4">
					<div class="flex items-start gap-2">
						<KindIcon kind={connection.type} family={provider?.family} size={18} />
						<div class="min-w-0 flex-1">
							<div class="truncate text-[13px] font-semibold">{connection.name}</div>
							<div class="truncate font-mono text-[10px] text-text-muted">
								{provider?.label ?? connection.type}{summary(connection)
									? ` · ${summary(connection)}`
									: ''}
							</div>
						</div>
					</div>

					<div class="flex flex-wrap gap-1">
						{#if connection.credentials_read_only}
							<Badge tone="success">Read-only credentials</Badge>
						{/if}
						{#if connection.has_credentials}
							<Badge tone="neutral">Credentials stored</Badge>
						{/if}
					</div>

					{#if ownEngines.length > 0}
						<div class="text-[11px]">
							<span class="text-text-muted">Engine</span>
							{#each ownEngines as engine (engine.id)}
								<span class="ml-1 inline-flex items-center gap-1 font-medium">
									<KindIcon kind={engine.kind} family="engine" size={13} />
									{engine.name}
								</span>
							{/each}
						</div>
					{/if}

					{#if catalogsHere.length > 0}
						<ul class="flex flex-col gap-1 text-[11px]">
							{#each catalogsHere as item (item.catalog.id)}
								<li class="flex items-center gap-1.5">
									<span class="font-mono">{item.catalog.name}</span>
									{#if item.catalog.metadata_sync_status}
										<StatusBadge
											kind="sync"
											status={syncStatus(item.catalog.metadata_sync_status)}
											title="Metadata freshness"
										/>
									{/if}
									{#if item.catalog.metadata_synced_at}
										<span class="text-text-muted">
											<RelativeTime at={item.catalog.metadata_synced_at} />
										</span>
									{/if}
									{#if item.bindings.length > 0}
										<span class="text-text-muted">
											· {item.bindings.length} binding{item.bindings.length === 1 ? '' : 's'}
										</span>
									{/if}
								</li>
							{/each}
						</ul>
					{/if}

					<div class="mt-auto flex flex-wrap gap-1.5">
						<Button
							size="sm"
							onclick={() => {
								editing = connection;
								editOpen = true;
							}}>Edit</Button
						>
						<Button
							size="sm"
							loading={testingId === connection.id}
							onclick={() => test(connection)}
						>
							Test
						</Button>
						{#if provider && canQueryDirectly(provider) && ownEngines.length === 0}
							<Button
								size="sm"
								loading={registeringId === connection.id}
								onclick={() => registerEngine(connection)}
							>
								Register engine
							</Button>
						{/if}
						{#if compatibleEngines(connection.type).length > 0}
							<Button size="sm" onclick={() => openAdd(connection)}>Add catalog</Button>
						{/if}
						<span
							title={ownEngines.length > 0 ? 'Delete the engine first' : ''}
							class="inline-flex"
						>
							<Button
								size="sm"
								variant="danger"
								disabled={ownEngines.length > 0}
								onclick={() => {
									deleting = connection;
									deleteOpen = true;
								}}
							>
								Delete
							</Button>
						</span>
					</div>
				</article>
			{/each}
		</div>
	{/if}
</div>

{#if editing}
	<EditConnectionDialog
		bind:open={editOpen}
		connection={editing}
		{providers}
		onsaved={(updated) => {
			onSaved(updated);
			editing = null;
		}}
	/>
{/if}

{#if deleting}
	<ConfirmDialog
		bind:open={deleteOpen}
		title="Remove connection"
		resourceName={deleting.name}
		consequences={consequences(deleting)}
		confirmLabel="Remove connection"
		typeToConfirm
		onconfirm={remove}
	/>
{/if}

{#if adding}
	{@const connectors = connectorsFor(adding)}
	<Modal
		bind:open={addOpen}
		title={`Add catalog to ${adding.name}`}
		onclose={() => {
			adding = null;
		}}
	>
		<div class="flex flex-col gap-3">
			<Input label="Catalog or warehouse name" bind:value={addName} placeholder="e.g. analytics" />
			<p class="text-[10px] text-text-muted">
				It becomes the name used in SQL on the federated engine.
			</p>
			{#if connectors.length > 1}
				<Select
					label="Connector"
					bind:value={addConnector}
					options={connectors.map((item) => ({ value: item, label: item }))}
				/>
			{/if}
		</div>
		{#snippet footer()}
			<div class="flex justify-end gap-2">
				<Button onclick={() => (adding = null)} disabled={addingBusy}>Cancel</Button>
				<Button
					variant="primary"
					loading={addingBusy}
					disabled={addingBusy || !addName.trim()}
					onclick={addCatalog}
				>
					Add catalog
				</Button>
			</div>
		{/snippet}
	</Modal>
{/if}
