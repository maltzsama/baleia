<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import ActivityPanel from '$lib/audit/ActivityPanel.svelte';
	import {
		bindingProjection,
		bindingRemediation,
		bindingSupport,
		catalogGrants,
		catalogImpact,
		createBinding,
		deleteBinding,
		deleteCatalog,
		getBinding,
		getCatalog,
		grantCatalog,
		listSyncRuns,
		revokeCatalog,
		syncBinding,
		syncMetadata,
		updateBinding,
		updateCatalogMeta,
		type BindingProjection,
		type BindingSupport,
		type CatalogBinding,
		type CatalogGrant,
		type CatalogImpact,
		type CatalogSummary,
		type MetadataSyncRun
	} from '$lib/catalogs/admin';
	import { listCatalogBindings, listGrantableWorkspaces } from '$lib/connections/api';
	import { listEngines, type Engine } from '$lib/engines/admin';
	import { poll, type PollController } from '$lib/util/poll';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Drawer from '$lib/ui/Drawer.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';
	import EngineBadge from '$lib/ui/patterns/EngineBadge.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import KeyValueEditor from '$lib/ui/patterns/KeyValueEditor.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';

	type Tab = 'overview' | 'bindings' | 'metadata' | 'access' | 'activity';

	const catalogId = $derived(page.params.catalog_id ?? '');
	const engineNameRe = /^[a-z][a-z0-9_]{0,62}$/;

	let tab = $state<Tab>('overview');
	let catalog = $state<CatalogSummary | null>(null);
	let bindings = $state<CatalogBinding[]>([]);
	let engines = $state<Engine[]>([]);
	let runs = $state<MetadataSyncRun[]>([]);
	let grants = $state<CatalogGrant[]>([]);
	let impact = $state<CatalogImpact | null>(null);
	let workspaces = $state<{ id: string; name: string }[]>([]);
	let loading = $state(true);
	let error = $state('');

	let editOpen = $state(false);
	let editName = $state('');
	let editDescription = $state('');
	let saving = $state(false);
	let editError = $state('');

	let deleteOpen = $state(false);

	let addOpen = $state(false);
	let supports = $state<Record<string, Record<string, BindingSupport>>>({});
	let addEngineId = $state('');
	let addConnector = $state('');
	let addName = $state('');
	let addProps = $state<Record<string, string>>({});
	let addError = $state('');
	let adding = $state(false);

	let editBinding = $state<CatalogBinding | null>(null);
	let editBindingOpen = $state(false);
	let bindingName = $state('');
	let bindingEnabled = $state(true);
	let bindingProps = $state<Record<string, string>>({});
	let bindingError = $state('');
	let bindingSaving = $state(false);

	let projection = $state<BindingProjection | null>(null);
	let projectionOpen = $state(false);
	let projectionLoading = $state(false);

	let syncing = $state(false);
	let syncError = $state('');

	let grantOpen = $state(false);
	let grantWorkspaceId = $state('');
	let grantPermission = $state<'read' | 'write'>('read');
	let granting = $state(false);
	let grantError = $state('');
	let revokeOpen = $state(false);
	let revokeWorkspace = $state<CatalogGrant['workspace'] | null>(null);

	let polls: PollController<unknown>[] = [];

	const providerKind = $derived(catalog?.provider_kind ?? '');
	const federated = $derived(engines.filter((engine) => engine.class === 'federated'));
	const support = $derived(supports[addEngineId] ?? {});
	const bindableEngines = $derived(
		federated.filter((engine) => (providerKind ? !!supports[engine.id]?.[providerKind] : false))
	);
	const connectors = $derived(providerKind ? (support[providerKind]?.connectors ?? []) : []);
	const reservedNames = $derived(providerKind ? (support[providerKind]?.reserved_names ?? []) : []);

	function stopPolls() {
		for (const controller of polls) controller.stop();
		polls = [];
	}

	function engineFor(binding: CatalogBinding): Engine | undefined {
		return engines.find((engine) => engine.id === binding.engine_id);
	}

	async function load() {
		if (!catalogId) return;
		loading = true;
		error = '';
		try {
			catalog = await getCatalog(catalogId);
			[engines, bindings, runs, grants, impact] = await Promise.all([
				listEngines(),
				listCatalogBindings(catalogId),
				listSyncRuns(catalogId),
				catalogGrants(catalogId),
				catalogImpact(catalogId)
			]);
			workspaces = (await listGrantableWorkspaces()).map((workspace) => ({
				id: workspace.id,
				name: workspace.name
			}));
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	function selectTab(next: Tab) {
		tab = next;
		window.history.replaceState(null, '', `${page.url.pathname}?tab=${next}`);
	}

	function normalizeName(name: string): string {
		let out = name
			.toLowerCase()
			.replace(/[^a-z0-9_]+/g, '_')
			.replace(/^_+|_+$/g, '');
		if (!/^[a-z]/.test(out)) out = `c_${out}`;
		return out.slice(0, 63);
	}

	async function openAdd() {
		addError = '';
		addOpen = true;
		addEngineId = '';
		addConnector = '';
		addProps = {};
		if (!catalog) return;
		addName = normalizeName(catalog.name ?? '');
		const loaded = await Promise.all(
			federated.map(async (engine) => {
				try {
					return [engine.id, await bindingSupport(engine.id)] as const;
				} catch {
					return [engine.id, {}] as const;
				}
			})
		);
		supports = Object.fromEntries(loaded) as Record<string, Record<string, BindingSupport>>;
		const candidates = federated.filter((engine) => !!supports[engine.id]?.[providerKind]);
		const first = candidates[0];
		if (first) {
			addEngineId = first.id;
			addConnector = supports[first.id]?.[providerKind]?.connectors[0] ?? '';
		}
	}

	async function submitAdd() {
		if (!catalog || !addEngineId || !addName || !addConnector) {
			addError = m.catalogs_choose_an_engine_connector_and_name();
			return;
		}
		if (!engineNameRe.test(addName)) {
			addError = 'Name must match ^[a-z][a-z0-9_]{0,62}$.';
			return;
		}
		if (reservedNames.includes(addName)) {
			addError = m.catalogs_that_name_is_reserved_by_the_engine();
			return;
		}
		adding = true;
		addError = '';
		try {
			const created = await createBinding(catalog.id ?? catalogId, {
				engine_id: addEngineId,
				engine_catalog_name: addName,
				connector: addConnector,
				extra_props: Object.keys(addProps).length > 0 ? addProps : undefined
			});
			bindings = [...bindings, created];
			addOpen = false;
			void pollBinding(created.id);
		} catch (e) {
			addError = e instanceof Error ? e.message : String(e);
		} finally {
			adding = false;
		}
	}

	async function pollBinding(id: string) {
		const controller = poll(
			async () => {
				const current = await getBinding(id);
				bindings = bindings.map((binding) => (binding.id === id ? current : binding));
				return current;
			},
			{
				until: (binding) =>
					['active', 'error', 'disabled'].includes(binding.lifecycle_state ?? 'pending'),
				intervals: [1000, 2000, 5000]
			}
		);
		polls.push(controller);
		await controller.done;
	}

	function openEdit(binding: CatalogBinding) {
		editBinding = binding;
		bindingName = binding.engine_catalog_name;
		bindingEnabled = binding.enabled;
		bindingProps = { ...(binding.extra_props ?? {}) };
		bindingError = '';
		editBindingOpen = true;
	}

	async function submitEdit() {
		if (!editBinding) return;
		if (!engineNameRe.test(bindingName)) {
			bindingError = 'Name must match ^[a-z][a-z0-9_]{0,62}$.';
			return;
		}
		bindingSaving = true;
		bindingError = '';
		try {
			const updated = await updateBinding(editBinding.id, {
				engine_catalog_name: bindingName,
				extra_props: bindingProps,
				enabled: bindingEnabled
			});
			bindings = bindings.map((binding) => (binding.id === updated.id ? updated : binding));
			editBinding = null;
			editBindingOpen = false;
			void pollBinding(updated.id);
		} catch (e) {
			bindingError = e instanceof Error ? e.message : String(e);
		} finally {
			bindingSaving = false;
		}
	}

	async function toggleBinding(binding: CatalogBinding) {
		try {
			const updated = await updateBinding(binding.id, { enabled: !binding.enabled });
			bindings = bindings.map((item) => (item.id === updated.id ? updated : item));
			void pollBinding(updated.id);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function retry(binding: CatalogBinding) {
		try {
			const updated = await syncBinding(binding.id);
			bindings = bindings.map((item) => (item.id === updated.id ? updated : item));
			void pollBinding(updated.id);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function removeBinding(binding: CatalogBinding) {
		try {
			await deleteBinding(binding.id);
			bindings = bindings.filter((item) => item.id !== binding.id);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function openProjection(binding: CatalogBinding) {
		projection = null;
		projectionOpen = true;
		projectionLoading = true;
		try {
			projection = await bindingProjection(binding.id);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
			projectionOpen = false;
		} finally {
			projectionLoading = false;
		}
	}

	async function syncNow() {
		if (!catalogId) return;
		syncing = true;
		syncError = '';
		try {
			await syncMetadata(catalogId);
			runs = await listSyncRuns(catalogId);
			const controller = poll(
				async () => {
					runs = await listSyncRuns(catalogId);
					return runs[0];
				},
				{ until: (run) => !run || run.status !== 'running', intervals: [1000, 2000, 5000] }
			);
			polls.push(controller);
			await controller.done;
			catalog = await getCatalog(catalogId);
		} catch (e) {
			syncError = e instanceof Error ? e.message : String(e);
		} finally {
			syncing = false;
		}
	}

	function openEditCatalog() {
		if (!catalog) return;
		editName = catalog.name ?? '';
		editDescription = catalog.description ?? '';
		editError = '';
		editOpen = true;
	}

	async function submitEditCatalog() {
		if (!catalogId) return;
		saving = true;
		editError = '';
		try {
			await updateCatalogMeta(catalogId, {
				name: editName.trim(),
				description: editDescription.trim() || null
			});
			catalog = await getCatalog(catalogId);
			editOpen = false;
		} catch (e) {
			editError = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}

	async function confirmDelete() {
		await deleteCatalog(catalogId);
		window.location.href = '/catalogs';
	}

	async function submitGrant() {
		if (!catalogId || !grantWorkspaceId) return;
		granting = true;
		grantError = '';
		try {
			await grantCatalog(grantWorkspaceId, catalogId, grantPermission);
			grants = await catalogGrants(catalogId);
			grantOpen = false;
		} catch (e) {
			grantError = e instanceof Error ? e.message : String(e);
		} finally {
			granting = false;
		}
	}

	async function revoke(workspaceId: string) {
		await revokeCatalog(workspaceId, catalogId);
		grants = await catalogGrants(catalogId);
	}

	function consequences(): string[] {
		if (!impact) return [];
		return [
			`${impact.bindings} binding(s) will be deprovisioned.`,
			`${impact.grants} workspace grant(s) and ${impact.saved_queries_targeting} saved quer(ies) target it.`,
			`${impact.running_executions} execution(s) are running now.`
		];
	}

	onMount(() => {
		const requested = page.url.searchParams.get('tab');
		if (
			requested &&
			['overview', 'bindings', 'metadata', 'access', 'activity'].includes(requested)
		) {
			tab = requested as Tab;
		}
		void load();
		return stopPolls;
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	{#if loading && !catalog}
		<div class="flex flex-col gap-2" role="status" aria-label={m.catalogs_loading_the_catalog()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-14 w-full" />
			{/each}
		</div>
	{:else if error && !catalog}
		<ErrorState title="Could not load the catalog" message={error} onretry={load} />
	{:else if catalog}
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{catalog.name ?? catalogId}</h1>
				<p class="mt-1 text-[12px] text-slate-600 dark:text-slate-300">
					{catalog.origin} · {catalog.provider_kind ?? '—'} · {catalog.connection?.name ??
						m.catalogs_connection_hidden()}
					{#if catalog.needs_attention}
						· <Badge tone="warning">Needs attention</Badge>
					{/if}
				</p>
			</div>
			<div class="flex flex-wrap gap-2">
				<Button onclick={openEditCatalog}>Edit</Button>
				<Button variant="danger" onclick={() => (deleteOpen = true)}>Delete</Button>
			</div>
		</div>

		<div class="mb-4 flex gap-1 border-b border-border" role="tablist" aria-label="Catalog tabs">
			{#each [['overview', 'Overview'], ['bindings', 'Bindings'], ['metadata', 'Metadata'], ['access', m.catalogs_access()], ['activity', 'Activity']] as [value, label] (value)}
				<button
					type="button"
					role="tab"
					aria-selected={tab === value}
					class="-mb-px border-b-2 px-3 py-1.5 text-[12px] font-semibold {tab === value
						? 'border-primary text-primary'
						: 'border-transparent text-slate-600 hover:text-text-main dark:text-slate-300'}"
					onclick={() => selectTab(value as Tab)}
				>
					{label}
				</button>
			{/each}
		</div>

		{#if tab === 'overview'}
			<div class="grid gap-4 lg:grid-cols-2">
				<div class="rounded-lg border border-border bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold">Catalog</h2>
					<dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
						<dt class="text-slate-600 dark:text-slate-300">Description</dt>
						<dd>{catalog.description ?? '—'}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Origin</dt>
						<dd>{catalog.origin}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Source ref</dt>
						<dd class="font-mono">{catalog.source_ref ?? '—'}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Namespaces</dt>
						<dd>{catalog.namespace_count ?? 0}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Tables</dt>
						<dd>{catalog.table_count ?? 0}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Enabled</dt>
						<dd>{catalog.enabled ? 'Yes' : 'No'}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Created</dt>
						<dd>{catalog.created_at ? new Date(catalog.created_at).toLocaleString() : '—'}</dd>
					</dl>
					<div class="mt-3">
						<button
							type="button"
							class="text-[12px] text-primary hover:underline"
							onclick={() => (window.location.href = `/data?catalog=${catalogId}`)}
						>
							Open in explorer →
						</button>
					</div>
				</div>
				<div class="rounded-lg border border-border bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold">Metadata freshness</h2>
					<p class="mt-2 text-[11px]">
						{#if catalog.metadata_synced_at}
							Last pass <RelativeTime at={catalog.metadata_synced_at} />
						{:else}
							No metadata pass yet.
						{/if}
					</p>
					{#if catalog.metadata_sync_status}
						<StatusBadge kind="metadataSync" status={catalog.metadata_sync_status} />
					{/if}
					{#if catalog.metadata_sync_error_code}
						<p class="mt-2 font-mono text-[11px] text-danger">{catalog.metadata_sync_error_code}</p>
					{/if}
					<div class="mt-3">
						<Button loading={syncing} disabled={syncing} onclick={syncNow}>Sync now</Button>
					</div>
				</div>
			</div>
		{:else if tab === 'bindings'}
			<div class="mb-3 flex items-center justify-between">
				<p class="text-[12px] text-slate-600 dark:text-slate-300">
					Adopted and native bindings are read-only: Baleia never changes or drops them.
				</p>
				<Button variant="primary" onclick={openAdd}>Add binding</Button>
			</div>
			{#if error}
				<p class="mb-2 text-[11px] text-danger" role="alert">{error}</p>
			{/if}
			{#if bindings.length === 0}
				<EmptyState
					title="No bindings"
					description="Bind this catalog into a federated engine to query it."
				/>
			{:else}
				<div class="flex flex-col gap-3">
					{#each bindings as binding (binding.id)}
						{@const engine = engineFor(binding)}
						<div class="rounded-lg border border-border bg-bg-panel p-4">
							<div class="flex flex-wrap items-start justify-between gap-2">
								<div>
									<div class="flex flex-wrap items-center gap-2">
										{#if engine}
											<EngineBadge {engine} />
										{/if}
										<span class="flex items-center gap-1 font-mono text-[12px]">
											{binding.engine_catalog_name}<CopyButton
												value={binding.engine_catalog_name}
												label="catalog name"
											/>
										</span>
										<span
											title={binding.mode === 'managed'
												? m.catalogs_created_and_maintained_by_baleia()
												: binding.mode === 'adopted'
													? m.catalogs_exists_in_the_engine_not_created_by_baleia()
													: m.catalogs_a_catalog_of_a_direct_engine()}
										>
											<Badge tone={binding.mode === 'managed' ? 'info' : 'neutral'}>
												{binding.mode}
											</Badge>
										</span>
									</div>
									<div class="mt-1 flex flex-wrap items-center gap-1.5">
										<StatusBadge
											kind="binding"
											status={binding.lifecycle_state ?? (binding.enabled ? 'active' : 'disabled')}
										/>
										{#if binding.sync_status}
											<Badge
												tone={binding.sync_status === 'synced'
													? 'success'
													: binding.sync_status === 'error'
														? 'danger'
														: 'neutral'}
											>
												{binding.sync_status}
											</Badge>
										{/if}
										{#if binding.connector}
											<span class="text-[10px] text-slate-600 dark:text-slate-300"
												>connector {binding.connector}</span
											>
										{/if}
									</div>
								</div>
								{#if binding.mode === 'managed'}
									<div class="flex flex-wrap gap-1.5">
										<Button size="sm" onclick={() => retry(binding)}>Re-apply</Button>
										<Button size="sm" onclick={() => openEdit(binding)}>Edit</Button>
										<Button size="sm" onclick={() => toggleBinding(binding)}>
											{binding.enabled ? 'Disable' : 'Enable'}
										</Button>
										<Button size="sm" variant="danger" onclick={() => removeBinding(binding)}
											>Delete</Button
										>
									</div>
								{/if}
							</div>
							{#if binding.last_error_code || binding.last_error_message}
								<div class="mt-3">
									<ErrorPanel
										error={{
											code: binding.last_error_code ?? 'engine_error',
											message:
												binding.last_error_message ?? bindingRemediation(binding.last_error_code)
										}}
									/>
									<p class="mt-1 text-[11px] text-slate-600 dark:text-slate-300">
										{bindingRemediation(binding.last_error_code)}
									</p>
								</div>
							{/if}
							<button
								type="button"
								class="mt-2 text-[11px] text-primary hover:underline"
								onclick={() => openProjection(binding)}
							>
								Show projection
							</button>
						</div>
					{/each}
				</div>
			{/if}
		{:else if tab === 'metadata'}
			<div class="mb-3 flex items-center justify-between">
				<p class="text-[12px] text-slate-600 dark:text-slate-300">
					Every pass reads namespaces, tables and columns from the catalog's source or one of its
					engines.
				</p>
				<Button loading={syncing} disabled={syncing} onclick={syncNow}>Sync now</Button>
			</div>
			{#if syncError}
				<p class="mb-2 text-[11px] text-danger" role="alert">{syncError}</p>
			{/if}
			{#if runs.length === 0}
				<EmptyState
					title="No metadata run"
					description="Start a sync to build the catalog snapshot."
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">Status</th>
								<th class="px-3 py-2">Started</th>
								<th class="px-3 py-2">Finished</th>
								<th class="px-3 py-2">Seen / changed / removed</th>
								<th class="px-3 py-2">Source</th>
								<th class="px-3 py-2">Error</th>
							</tr>
						</thead>
						<tbody>
							{#each runs as run (run.id)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-3 py-1.5"><StatusBadge kind="execution" status={run.status} /></td>
									<td class="px-3 py-1.5"><RelativeTime at={run.started_at} /></td>
									<td class="px-3 py-1.5">
										{#if run.finished_at}<RelativeTime at={run.finished_at} />{:else}—{/if}
									</td>
									<td class="px-3 py-1.5">
										{run.tables_seen ?? '—'} / {run.tables_changed ?? '—'} / {run.tables_removed ??
											'—'}
									</td>
									<td
										class="px-3 py-1.5"
										title={run.source_path === 'provider'
											? m.catalogs_read_from_the_catalog_source()
											: m.catalogs_introspected_through_a_binding()}
									>
										{run.source_path}
									</td>
									<td class="px-3 py-1.5 font-mono">{run.error_code ?? '—'}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{:else if tab === 'access'}
			<div class="mb-3 flex items-center justify-between">
				<p class="text-[12px] text-slate-600 dark:text-slate-300">
					Workspaces granted this catalog. Owners and administrators always manage it.
				</p>
				<Button variant="primary" onclick={() => (grantOpen = true)}>Grant access</Button>
			</div>
			{#if grants.length === 0}
				<EmptyState title="No grants" description="No workspace has access to this catalog yet." />
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">Workspace</th>
								<th class="px-3 py-2">Permission</th>
								<th class="px-3 py-2">Granted</th>
								<th class="px-3 py-2 text-right">Actions</th>
							</tr>
						</thead>
						<tbody>
							{#each grants as grant (grant.workspace.id)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-3 py-1.5">{grant.workspace.name}</td>
									<td class="px-3 py-1.5"><Badge tone="info">{grant.permission}</Badge></td>
									<td class="px-3 py-1.5"><RelativeTime at={grant.granted_at} /></td>
									<td class="px-3 py-1.5 text-right">
										<button
											type="button"
											class="text-danger hover:underline"
											onclick={() => {
												revokeWorkspace = grant.workspace;
												revokeOpen = true;
											}}
										>
											Revoke
										</button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{:else if tab === 'activity'}
			<ActivityPanel resourceType="catalog" resourceId={catalogId} title="Catalog activity" />
		{/if}
	{/if}
</div>

<Modal bind:open={editOpen} title={m.catalogs_edit_catalog()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<Input label={m.common_name()} bind:value={editName} />
		<Input label={m.common_description()} bind:value={editDescription} />
		<p class="text-[11px] text-amber-700 dark:text-amber-300">
			{m.catalogs_bundles_and_metrics_reference_catalogs_by()}
		</p>
		{#if editError}<p class="text-danger" role="alert">{editError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (editOpen = false)} disabled={saving}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={saving} disabled={saving} onclick={submitEditCatalog}
				>{m.common_save()}</Button
			>
		</div>
	{/snippet}
</Modal>

<Modal bind:open={addOpen} title={m.catalogs_add_binding()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<Select
			label={m.common_engine()}
			bind:value={addEngineId}
			options={bindableEngines.map((engine) => ({ value: engine.id, label: engine.name }))}
			onchange={() => (addConnector = support[providerKind]?.connectors[0] ?? '')}
		/>
		<Select
			label={m.common_connector()}
			bind:value={addConnector}
			options={connectors.map((connector) => ({ value: connector, label: connector }))}
		/>
		<Input label={m.catalogs_name_in_the_engine()} bind:value={addName} class="font-mono" />
		<div>
			<p class="mb-1 text-[11px] font-semibold">{m.catalogs_extra_properties()}</p>
			<KeyValueEditor
				bind:value={addProps}
				protectedKeys={providerKind
					? (supports[editBinding?.engine_id ?? '']?.[providerKind]?.protected_key_patterns ?? [])
					: []}
			/>
		</div>
		{#if addError}<p class="text-danger" role="alert">{addError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (addOpen = false)} disabled={adding}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={adding} disabled={adding} onclick={submitAdd}
				>{m.catalogs_bind()}</Button
			>
		</div>
	{/snippet}
</Modal>

<Modal
	bind:open={editBindingOpen}
	title={m.catalogs_edit_binding()}
	onclose={() => (editBinding = null)}
>
	<div class="flex flex-col gap-3 text-[12px]">
		<Input label={m.catalogs_name_in_the_engine()} bind:value={bindingName} class="font-mono" />
		<label class="flex items-center gap-2">
			<input type="checkbox" bind:checked={bindingEnabled} />
			{m.common_enabled()}
		</label>
		<div>
			<p class="mb-1 text-[11px] font-semibold">{m.catalogs_extra_properties()}</p>
			<KeyValueEditor
				bind:value={bindingProps}
				protectedKeys={providerKind
					? (supports[editBinding?.engine_id ?? '']?.[providerKind]?.protected_key_patterns ?? [])
					: []}
			/>
		</div>
		{#if bindingError}<p class="text-danger" role="alert">{bindingError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button
				onclick={() => {
					editBinding = null;
					editBindingOpen = false;
				}}
				disabled={bindingSaving}>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				loading={bindingSaving}
				disabled={bindingSaving}
				onclick={submitEdit}>{m.common_save()}</Button
			>
		</div>
	{/snippet}
</Modal>

<Modal bind:open={grantOpen} title={m.common_grant_catalog_access()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<Select
			label={m.common_workspace()}
			bind:value={grantWorkspaceId}
			options={workspaces.map((workspace) => ({ value: workspace.id, label: workspace.name }))}
		/>
		<Select
			label={m.common_permission()}
			bind:value={grantPermission}
			options={[
				{ value: 'read', label: 'Read' },
				{ value: 'write', label: 'Write' }
			]}
		/>
		{#if grantError}<p class="text-danger" role="alert">{grantError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (grantOpen = false)} disabled={granting}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={granting} disabled={granting} onclick={submitGrant}
				>{m.common_grant()}</Button
			>
		</div>
	{/snippet}
</Modal>

<Drawer size="lg" bind:open={projectionOpen} title={m.catalogs_binding_projection()}>
	{#if projectionLoading}
		<Skeleton class="h-24 w-full" />
	{:else if projection}
		<p class="text-[11px] text-slate-600 dark:text-slate-300">
			Connector <span class="font-mono">{projection.connector}</span>
			{#if projection.version}· version <span class="font-mono">{projection.version}</span>{/if}
		</p>
		<table class="mt-3 w-full text-left text-[11px]">
			<thead class="text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300">
				<tr><th class="py-1">Property</th><th class="py-1">Value</th></tr>
			</thead>
			<tbody>
				{#each Object.entries(projection.properties) as [key, value] (key)}
					<tr class="border-t border-border/60">
						<td class="py-1 font-mono">{key}</td>
						<td class="py-1 font-mono">{value}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</Drawer>

<ConfirmDialog
	bind:open={revokeOpen}
	title={m.common_revoke_catalog_access()}
	resourceName={revokeWorkspace?.name ?? ''}
	confirmLabel="Revoke"
	consequences={['Members of this workspace lose access to the catalog.']}
	onconfirm={async () => {
		if (revokeWorkspace) await revoke(revokeWorkspace.id);
	}}
/>

<ConfirmDialog
	bind:open={deleteOpen}
	title={m.catalogs_delete_catalog()}
	resourceName={catalog?.name ?? ''}
	confirmLabel="Delete catalog"
	typeToConfirm
	consequences={consequences()}
	onconfirm={confirmDelete}
/>
