<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { plural } from '$lib/i18n/plural';
	import { authStore } from '$lib/stores/auth.svelte';
	import { canGlobal } from '$lib/authz/can';
	import { eachLimited } from '$lib/util/limit';
	import { onMount } from 'svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Drawer from '$lib/ui/Drawer.svelte';
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
	import ActivityPanel from '$lib/audit/ActivityPanel.svelte';
	import {
		canQueryDirectly,
		normalizeCatalogName,
		normalizeEngineName
	} from '$lib/connections/providers';

	// Cards whose catalogs load at once; the rest wait their turn.
	const CATALOG_LOOKUPS = 4;

	/** A catalog and its bindings; null bindings means they failed to load. */
	type CatalogInfo = { catalog: Catalog; bindings: CatalogBinding[] | null };
	/** Each card fills in on its own (#477): loading, loaded or failed. */
	type CardState = { status: 'loading' | 'ready' | 'error'; error: string };

	let connections = $state<CatalogConnection[]>([]);
	let providers = $state<ConnectionProvider[]>([]);
	let engines = $state<Engine[]>([]);
	let support = $state<Record<string, Record<string, BindingSupport>>>({});
	let catalogs = $state<Record<string, CatalogInfo[]>>({});
	let cards = $state<Record<string, CardState>>({});
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
	let activity = $state<CatalogConnection | null>(null);
	let activityOpen = $state(false);

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
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
		// The list is on screen; each card's catalogs and bindings follow, a
		// few at a time, without holding the others back.
		if (!error) {
			cards = Object.fromEntries(
				connections.map((connection) => [connection.id, { status: 'loading', error: '' }])
			);
			void eachLimited(connections, CATALOG_LOOKUPS, loadCatalogs);
		}
	}

	async function loadCatalogs(connection: CatalogConnection) {
		cards = { ...cards, [connection.id]: { status: 'loading', error: '' } };
		try {
			const list = await listConnectionCatalogs(connection.id);
			const withBindings = await Promise.all(
				list.map(async (catalog) => ({
					catalog,
					// A failed bindings lookup is not "no bindings".
					bindings: catalog.id ? await listCatalogBindings(catalog.id).catch(() => null) : []
				}))
			);
			catalogs = { ...catalogs, [connection.id]: withBindings };
			cards = { ...cards, [connection.id]: { status: 'ready', error: '' } };
		} catch (e) {
			cards = {
				...cards,
				[connection.id]: { status: 'error', error: e instanceof Error ? e.message : String(e) }
			};
		}
	}

	async function test(connection: CatalogConnection) {
		testingId = connection.id;
		try {
			const result = await testStored(connection.id);
			if (result.success) {
				toasts.success(
					m.connections_reachable({ name: connection.name, latency: result.latency_ms })
				);
			} else {
				toasts.error(
					`${connection.name}: ${result.error_message ?? m.connections_the_test_failed()}`
				);
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
			toasts.success(m.connections_engine_registered({ name: engine.name }));
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
			toasts.success(m.connections_catalog_created({ addName }));
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
		toasts.success(m.connections_connection_saved());
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
		toasts.success(m.connections_connection_removed());
	}

	onMount(() => {
		void load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-5 flex items-end justify-between">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_connections()}</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{m.connections_providers_engines_catalogs_and_the_engines()}
			</p>
		</div>
		<Button variant="primary" onclick={() => (window.location.href = '/connections/new')}>
			{m.connections_new_connection()}
		</Button>
	</div>

	<div class="mb-5 flex items-center gap-2.5">
		<div class="w-64">
			<Input bind:value={search} placeholder={m.connections_search_connections()} />
		</div>
		<span class="ml-auto font-mono text-xs text-slate-600 dark:text-slate-300"
			>{filtered.length} {m.common_connection()}{filtered.length === 1 ? '' : 's'}</span
		>
	</div>

	{#if loading}
		<div
			class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,320px),1fr))] gap-3"
			aria-label={m.connections_loading_connections()}
		>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-40 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title={m.connections_could_not_load_connections()} message={error} onretry={load} />
	{:else if connections.length === 0}
		<!-- Nothing yet (#476): the first step, when the user may take it. -->
		<EmptyState
			title={m.connections_no_connections_yet()}
			description={m.connections_connect_a_catalog_manager_a_metastore_or_a_database()}
		>
			{#if canGlobal(authStore.user, 'create_connection')}
				<Button variant="primary" onclick={() => (window.location.href = '/connections/new')}>
					{m.connections_new_connection()}
				</Button>
			{/if}
		</EmptyState>
	{:else if filtered.length === 0}
		<!-- Connections exist; only the search found none. -->
		<div role="status">
			<EmptyState
				title={m.connections_no_connection_matches({ query: search })}
				description={m.common_try_other_words_or_clear_the_search()}
			>
				<Button onclick={() => (search = '')}>{m.common_clear_search()}</Button>
			</EmptyState>
		</div>
	{:else}
		<div class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,320px),1fr))] gap-3">
			{#each filtered as connection (connection.id)}
				{@const provider = providerOf(connection.type)}
				{@const ownEngines = enginesOf(connection.id)}
				{@const catalogsHere = catalogs[connection.id] ?? []}
				<article class="flex flex-col gap-3 rounded-lg border border-border bg-bg-panel p-4">
					<div class="flex items-start gap-2">
						<KindIcon kind={connection.type} family={provider?.family} size={18} />
						<div class="min-w-0 flex-1">
							<button
								type="button"
								class="truncate text-left text-[13px] font-semibold hover:underline"
								onclick={() => (window.location.href = `/connections/${connection.id}`)}
								>{connection.name}</button
							>
							<div class="truncate font-mono text-[10px] text-slate-600 dark:text-slate-300">
								{provider?.label ?? connection.type}{summary(connection)
									? ` · ${summary(connection)}`
									: ''}
							</div>
						</div>
					</div>

					<div class="flex flex-wrap gap-1">
						{#if connection.credentials_read_only}
							<Badge tone="success">{m.connections_read_only_credentials()}</Badge>
						{/if}
						{#if connection.has_credentials}
							<Badge tone="neutral">{m.connections_credentials_stored()}</Badge>
						{/if}
					</div>

					{#if ownEngines.length > 0}
						<div class="text-[11px]">
							<span class="text-slate-600 dark:text-slate-300">{m.connections_engine()}</span>
							{#each ownEngines as engine (engine.id)}
								<span class="ml-1 inline-flex items-center gap-1 font-medium">
									<KindIcon kind={engine.kind} family="engine" size={13} />
									{engine.name}
								</span>
							{/each}
						</div>
					{/if}

					{#if cards[connection.id]?.status === 'loading'}
						<div role="status" aria-label={m.connections_loading_catalogs()}>
							<Skeleton class="h-4 w-2/3" />
						</div>
					{:else if cards[connection.id]?.status === 'error'}
						<p class="flex flex-wrap items-center gap-2 text-[11px] text-danger" role="alert">
							{m.connections_could_not_load_catalogs()}
							<Button size="sm" onclick={() => loadCatalogs(connection)}>{m.common_retry()}</Button>
						</p>
					{:else if catalogsHere.length > 0}
						<ul class="flex flex-col gap-1 text-[11px]">
							{#each catalogsHere as item (item.catalog.id)}
								<li class="flex items-center gap-1.5">
									<span class="font-mono">{item.catalog.name}</span>
									{#if item.catalog.metadata_sync_status}
										<StatusBadge
											kind="sync"
											status={syncStatus(item.catalog.metadata_sync_status)}
											title={m.connections_metadata_freshness()}
										/>
									{/if}
									{#if item.catalog.metadata_synced_at}
										<span class="text-slate-600 dark:text-slate-300">
											<RelativeTime at={item.catalog.metadata_synced_at} />
										</span>
									{/if}
									{#if item.bindings === null}
										<span class="text-danger">· {m.connections_bindings_unavailable()}</span>
									{:else if item.bindings.length > 0}
										<span class="text-slate-600 dark:text-slate-300">
											· {plural(
												item.bindings.length,
												m.connections_binding_count_one,
												m.connections_binding_count_other
											)}
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
							}}>{m.connections_edit()}</Button
						>
						<Button
							size="sm"
							loading={testingId === connection.id}
							onclick={() => test(connection)}
						>
							{m.connections_test()}
						</Button>
						{#if provider && canQueryDirectly(provider) && ownEngines.length === 0}
							<Button
								size="sm"
								loading={registeringId === connection.id}
								onclick={() => registerEngine(connection)}
							>
								{m.connections_register_engine()}
							</Button>
						{/if}
						{#if compatibleEngines(connection.type).length > 0}
							<Button size="sm" onclick={() => openAdd(connection)}
								>{m.connections_add_catalog()}</Button
							>
						{/if}
						<Button
							size="sm"
							onclick={() => {
								activity = connection;
								activityOpen = true;
							}}
						>
							{m.connections_activity()}
						</Button>
						<span
							title={ownEngines.length > 0 ? m.connections_delete_the_engine_first() : ''}
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
								{m.connections_delete()}
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
		title={m.connections_remove_connection()}
		resourceName={deleting.name}
		consequences={consequences(deleting)}
		confirmLabel={m.connections_remove_connection()}
		typeToConfirm
		onconfirm={remove}
	/>
{/if}

{#if adding}
	{@const connectors = connectorsFor(adding)}
	<Modal
		bind:open={addOpen}
		title={m.connections_add_catalog_to({ name: adding.name })}
		onclose={() => {
			adding = null;
		}}
	>
		<div class="flex flex-col gap-3">
			<Input
				label={m.connections_catalog_or_warehouse_name()}
				bind:value={addName}
				placeholder={m.connections_e_g_analytics()}
			/>
			<p class="text-[10px] text-slate-600 dark:text-slate-300">
				{m.connections_it_becomes_the_name_used_in_sql_on_the_fed()}
			</p>
			{#if connectors.length > 1}
				<Select
					label={m.common_connector()}
					bind:value={addConnector}
					options={connectors.map((item) => ({ value: item, label: item }))}
				/>
			{/if}
		</div>
		{#snippet footer()}
			<div class="flex justify-end gap-2">
				<Button onclick={() => (adding = null)} disabled={addingBusy}>{m.common_cancel()}</Button>
				<Button
					variant="primary"
					loading={addingBusy}
					disabled={addingBusy || !addName.trim()}
					onclick={addCatalog}
				>
					{m.common_add_catalog()}
				</Button>
			</div>
		{/snippet}
	</Modal>
{/if}

{#if activity}
	<Drawer
		size="lg"
		bind:open={activityOpen}
		title={m.connections_activity_2({ name: activity.name })}
		onclose={() => {
			activity = null;
		}}
	>
		<ActivityPanel resourceType="connection" resourceId={activity.id} />
	</Drawer>
{/if}
