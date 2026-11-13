<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { plural } from '$lib/i18n/plural';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import KindIcon from '$lib/ui/patterns/KindIcon.svelte';
	import {
		bindingSupport,
		ConnectionRequestError,
		createConnection,
		grantCatalogAccess,
		listEngines,
		listGrantableWorkspaces,
		loadCategories,
		loadProviders,
		testDraft,
		type BindingSupport,
		type Catalog,
		type CatalogConnection,
		type ConnectionCategory,
		type ConnectionDraft,
		type Engine,
		type TestResult,
		type Workspace
	} from './api';
	import ProviderForm from './ProviderForm.svelte';
	import { DIRECT, FEDERATED, type AccessMode } from './accessModes';
	import {
		canDirect,
		canFederated,
		databaseName,
		initialValues,
		intentsFor,
		missingFields,
		needsCatalogs,
		normalizeCatalogName,
		normalizeEngineName,
		readsMetadata,
		isUsable,
		roleLabel,
		statusLabel,
		type ConnectionProvider,
		type Usage
	} from './providers';

	let {
		ondone
	}: {
		ondone?: (result: {
			connection: CatalogConnection;
			engine?: Engine;
			catalogs: Catalog[];
		}) => void;
	} = $props();

	let providers = $state<ConnectionProvider[]>([]);
	let categories = $state<ConnectionCategory[]>([]);
	let engines = $state<Engine[]>([]);
	let support = $state<Record<string, Record<string, BindingSupport>>>({});
	let loading = $state(true);
	let loadError = $state('');

	let step = $state(1);
	let provider = $state<ConnectionProvider | null>(null);
	let name = $state('');
	let values = $state<Record<string, string>>({});
	let secrets = $state<Record<string, string>>({});
	let credentialsReadOnly = $state(false);
	let accessMode = $state<AccessMode | null>(null);
	let bindEngineIds = $state<string[]>([]);
	let catalogName = $state('');

	let testing = $state(false);
	let testResult = $state<TestResult | null>(null);
	let testError = $state('');
	let testedSnapshot = $state('');

	let creating = $state(false);
	let createError = $state('');
	let created = $state<{
		connection: CatalogConnection;
		engine?: Engine;
		catalogs: Catalog[];
	} | null>(null);

	let grantable = $state<Workspace[]>([]);
	let grantCatalog = $state('');
	let grantWorkspace = $state('');
	let granting = $state(false);
	let grantError = $state<ConnectionRequestError | null>(null);
	let grants = $state<string[]>([]);

	onMount(async () => {
		try {
			const [providerList, categoryList, engineList] = await Promise.all([
				loadProviders(),
				loadCategories(),
				listEngines()
			]);
			providers = providerList;
			categories = categoryList;
			engines = engineList;
			const loaded = await Promise.all(
				engineList.map(async (engine) => {
					try {
						return [engine.id, await bindingSupport(engine.id)] as const;
					} catch {
						return [engine.id, {}] as const;
					}
				})
			);
			support = Object.fromEntries(loaded);
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	});

	// Categories come from the registry, ordered for display (ADR 0021 D.10 /
	// CR-13). A provider with an unknown or empty category is grouped last.
	const groups = $derived.by(() => {
		const out: {
			category: string;
			label: string;
			description: string;
			order: number;
			providers: ConnectionProvider[];
		}[] = [];
		for (const [index, category] of categories.entries()) {
			const inCategory = providers.filter((p) => p.category === category.category);
			if (inCategory.length === 0) continue;
			out.push({
				category: category.category,
				label: category.label,
				description: category.description ?? '',
				order: category.order ?? index,
				providers: inCategory
			});
		}
		const uncategorized = providers.filter(
			(p) => !categories.some((c) => c.category === p.category)
		);
		if (uncategorized.length > 0) {
			out.push({
				category: '',
				label: '',
				description: '',
				order: categories.length,
				providers: uncategorized
			});
		}
		out.sort((a, b) => a.order - b.order);
		return out;
	});

	const compatibleEngines = $derived(
		provider ? engines.filter((engine) => Boolean(support[engine.id]?.[provider!.kind])) : []
	);

	/** The data access modes the type declares, in registry order (D.2). */
	const modes = $derived(provider?.access_modes ?? []);

	const selectedSupport = $derived(
		provider && accessMode === FEDERATED && bindEngineIds.length > 0
			? support[bindEngineIds[0]]?.[provider.kind]
			: undefined
	);

	const connectors = $derived(selectedSupport?.connectors ?? []);
	const reservedNames = $derived(selectedSupport?.reserved_names ?? []);
	const isDatabase = $derived(
		provider ? provider.config_schema.some((f) => f.key === 'database') : false
	);
	const catalogOutputs = $derived(
		connectors.map((connector, i) => {
			const base = catalogName.trim() || name;
			const raw = i === 0 ? base : `${base}_${connector}`;
			return { name: raw, trino_catalog_name: normalizeCatalogName(raw), connector };
		})
	);
	const missing = $derived(provider ? missingFields(provider, values, secrets) : []);
	const snapshot = $derived(
		JSON.stringify({
			name,
			values,
			secrets,
			credentialsReadOnly,
			accessMode,
			bindEngineIds,
			catalogName
		})
	);
	const tested = $derived(testedSnapshot !== '' && testedSnapshot === snapshot);

	function choose(next: ConnectionProvider) {
		if (!isUsable(next)) return;
		provider = next;
		name = '';
		values = initialValues(next);
		secrets = {};
		credentialsReadOnly = false;
		// One declared mode is auto-selected; two are an explicit choice.
		const nextModes = next.access_modes ?? [];
		accessMode = nextModes.length === 1 ? nextModes[0] : null;
		bindEngineIds = [];
		catalogName = '';
		testResult = null;
		testError = '';
		testedSnapshot = '';
		createError = '';
		created = null;
		step = 2;
	}

	function toUsage() {
		if (isDatabase) {
			const db = databaseName(values);
			if (db) {
				catalogName = db;
				name = name || db;
			}
		}
		step = 3;
	}

	function toggleEngine(engineId: string, checked: boolean) {
		if (checked) {
			bindEngineIds = [...bindEngineIds, engineId];
		} else {
			bindEngineIds = bindEngineIds.filter((id) => id !== engineId);
		}
	}

	function usage(): Usage {
		return { accessMode, bindEngineIds };
	}

	async function runTest() {
		if (!provider) return;
		testing = true;
		testError = '';
		testResult = null;
		const snap = snapshot;
		try {
			const result = await testDraft({
				name,
				type: provider.kind,
				config: values,
				credentials: secrets,
				credentials_read_only: credentialsReadOnly,
				intents: intentsFor(provider, usage()),
				// The warehouse create() registers the catalog with, so the bind
				// check binds the same one.
				warehouse: needsCatalogs(usage()) ? catalogName.trim() || name : undefined
			});
			testResult = result;
			if (result.success) testedSnapshot = snap;
		} catch (e) {
			testError = e instanceof Error ? e.message : String(e);
		} finally {
			testing = false;
		}
	}

	async function create() {
		if (!provider || !tested) return;
		creating = true;
		createError = '';
		// A direct connection owns an engine named after it, so the name must
		// be usable as one; the user corrects it here instead of after the API
		// refused the request.
		if (accessMode === DIRECT && !normalizeEngineName(name)) {
			createError = m.connections_connectionwizard_connection_name_not_usable_as_engine();
			creating = false;
			return;
		}
		try {
			const draft: ConnectionDraft = {
				name,
				type: provider.kind,
				access_mode: accessMode ?? undefined,
				config: values,
				credentials: secrets,
				credentials_read_only: credentialsReadOnly
			};
			if (accessMode === FEDERATED) {
				const base = catalogName.trim() || name;
				const outputs = connectors.map((item, i) => {
					const raw = i === 0 ? base : `${base}_${item}`;
					return { name: raw, trino_catalog_name: normalizeCatalogName(raw) };
				});
				if (outputs.some((o) => !o.trino_catalog_name)) {
					throw new Error('A catalog name cannot be normalized.');
				}
				if (outputs.some((o) => reservedNames.includes(o.trino_catalog_name))) {
					throw new Error('A catalog name is reserved by the engine. Choose another name.');
				}
				draft.engine_ids = bindEngineIds;
				draft.catalog_registration = { catalogs: outputs, warehouse: base, description: '' };
			}
			// One request registers everything: the backend creates the engine
			// (direct) or the catalogs and bindings (federated) with the
			// connection, in one transaction.
			const result = await createConnection(draft);
			const catalogs: Catalog[] = result.catalogs;
			created = { connection: result, engine: result.engine ?? undefined, catalogs };
			try {
				grantable = await listGrantableWorkspaces();
			} catch {
				grantable = [];
			}
			grantCatalog = catalogs[0]?.id ?? '';
			grantWorkspace = grantable[0]?.id ?? '';
			step = 5;
		} catch (e) {
			createError = e instanceof Error ? e.message : String(e);
		} finally {
			creating = false;
		}
	}

	async function grant() {
		if (!grantCatalog || !grantWorkspace) return;
		granting = true;
		grantError = null;
		try {
			await grantCatalogAccess({
				catalogId: grantCatalog,
				workspaceId: grantWorkspace
			});
			const catalog = created?.catalogs.find((item) => item.id === grantCatalog);
			const workspace = grantable.find((item) => item.id === grantWorkspace);
			grants = [
				...grants,
				`${workspace?.name ?? grantWorkspace} → ${catalog?.name ?? m.connections_connectionwizard_the_catalog()}`
			];
		} catch (e) {
			grantError = e instanceof ConnectionRequestError ? e : null;
			if (!grantError) throw e;
		} finally {
			granting = false;
		}
	}

	function finish() {
		if (created) {
			ondone?.(created);
		} else {
			void goto(resolve('/connections'));
		}
	}

	function providerBadge(p: ConnectionProvider) {
		return statusLabel(p);
	}

	function providerBadgeTone(state: ConnectionProvider['status']['state']) {
		switch (state) {
			case 'experimental':
				return 'warning';
			case 'planned':
				return 'info';
			case 'unsupported':
			case 'not_applicable':
				return 'neutral';
			default:
				return 'neutral';
		}
	}
</script>

<div class="mx-auto flex w-full max-w-[880px] flex-col gap-5">
	{#if loading}
		<div
			class="flex flex-col gap-3"
			aria-label={m.connections_connectionwizard_loading_providers()}
		>
			<Skeleton class="h-8 w-1/3" />
			<Skeleton class="h-24 w-full" />
			<Skeleton class="h-24 w-full" />
		</div>
	{:else if loadError}
		<ErrorPanel error={{ code: 'service_unavailable', message: loadError }} />
	{:else if step === 1}
		<header>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">
				{m.connections_connectionwizard_new_connection()}
			</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{m.connections_connectionwizard_choose_what_you_are_connecting_baleia()}
			</p>
		</header>

		{#each groups as group (group.category)}
			<section class="flex flex-col gap-2">
				<div>
					<h2
						class="text-[11px] font-bold uppercase tracking-[0.7px] text-slate-600 dark:text-slate-300"
					>
						{group.label}
					</h2>
					<p class="text-[11px] text-slate-600 dark:text-slate-300">{group.description}</p>
				</div>
				<div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
					{#each group.providers as p (p.kind)}
						{@const disabled = !isUsable(p)}
						<button
							type="button"
							data-testid="provider-card"
							class="flex flex-col gap-2 rounded-lg border border-border bg-bg-panel p-3 text-left transition-colors duration-150 hover:border-primary disabled:cursor-not-allowed disabled:opacity-50"
							{disabled}
							onclick={() => choose(p)}
						>
							<div class="flex items-center gap-2">
								<KindIcon kind={p.kind} family={p.family} size={18} />
								<span class="flex-1 text-[13px] font-semibold">{p.label}</span>
								{#if providerBadge(p)}
									<Badge tone={providerBadgeTone(p.status.state)}>
										{providerBadge(p)}
									</Badge>
								{/if}
							</div>
							<div class="flex flex-wrap gap-1">
								{#each p.roles as role (role)}
									<Badge tone="info">{roleLabel(role)}</Badge>
								{/each}
							</div>
							{#if p.status.reason}
								<p class="text-[10px] text-slate-600 dark:text-slate-300">{p.status.reason}</p>
							{/if}
						</button>
					{/each}
				</div>
			</section>
		{/each}
	{:else if step === 2 && provider}
		<header class="flex items-center gap-3">
			<KindIcon kind={provider.kind} family={provider.family} size={22} />
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{provider.label}</h1>
				<p class="text-[12px] text-slate-600 dark:text-slate-300">
					{m.connections_connectionwizard_step_2_of_4_connection_settings()}
				</p>
			</div>
		</header>

		<div class="rounded-lg border border-border bg-bg-panel p-4">
			<Input
				label={m.connections_connectionwizard_connection_name()}
				bind:value={name}
				placeholder="e.g. analytics-prod"
			/>
			<div class="mt-3">
				<ProviderForm
					{provider}
					bind:values
					bind:secrets
					allowReferences={compatibleEngines.length > 0}
				/>
			</div>
			<div class="mt-3 rounded-md border border-border bg-bg-toolbar px-3 py-2.5">
				<Checkbox
					label={m.connections_connectionwizard_these_credentials_cannot_modify_data()}
					bind:checked={credentialsReadOnly}
				/>
				<p class="mt-1 text-[10px] leading-relaxed text-slate-600 dark:text-slate-300">
					{m.connections_connectionwizard_assert_this_when_the_account_is()}
				</p>
			</div>
		</div>

		{#if missing.length > 0}
			<p class="text-[11px] text-slate-600 dark:text-slate-300">
				{m.connections_connectionwizard_fill_the_required_fields_to_continue()}
			</p>
		{/if}

		<div class="flex justify-between">
			<Button onclick={() => (step = 1)}>{m.connections_connectionwizard_back()}</Button>
			<Button variant="primary" disabled={!name.trim() || missing.length > 0} onclick={toUsage}>
				{m.connections_connectionwizard_continue()}
			</Button>
		</div>
	{:else if step === 3 && provider}
		<header>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">
				{m.connections_connectionwizard_how_will_you_use_it()}
			</h1>
			<p class="text-[13px] text-slate-600 dark:text-slate-300">
				{m.connections_connectionwizard_step_3_of_4_baleia_can()}
			</p>
		</header>

		<div class="flex flex-col gap-3">
			{#if readsMetadata(provider)}
				<div class="flex items-start gap-2 rounded-lg border border-border bg-bg-panel p-3">
					<Badge tone="success">{m.connections_connectionwizard_always_on()}</Badge>
					<p class="text-[12px] text-slate-600 dark:text-slate-300">
						{m.connections_connectionwizard_baleia_lists_the_catalogs_schemas_and()}
					</p>
				</div>
			{/if}

			{#if modes.length === 2}
				<fieldset class="flex flex-col gap-2">
					<legend class="sr-only">{m.connections_connectionwizard_how_will_you_use_it()}</legend>
					{#if canDirect(provider)}
						<label
							class="flex items-start gap-2 rounded-lg border bg-bg-panel p-3 {accessMode === DIRECT
								? 'border-primary'
								: 'border-border'}"
						>
							<input
								type="radio"
								name="access_mode"
								value="direct"
								class="mt-0.5 h-4 w-4 accent-primary"
								checked={accessMode === DIRECT}
								onchange={() => (accessMode = DIRECT)}
							/>
							<span>
								<span class="block text-[13px] font-semibold"
									>{m.connections_connectionwizard_query_it_directly()}</span
								>
								<span class="block text-[11px] text-slate-600 dark:text-slate-300">
									{m.connections_connectionwizard_register_it_as_a_query_engine()}
								</span>
							</span>
						</label>
					{/if}
					{#if canFederated(provider)}
						<label
							class="flex items-start gap-2 rounded-lg border bg-bg-panel p-3 {accessMode ===
							FEDERATED
								? 'border-primary'
								: 'border-border'}"
						>
							<input
								type="radio"
								name="access_mode"
								value="federated"
								class="mt-0.5 h-4 w-4 accent-primary"
								checked={accessMode === FEDERATED}
								onchange={() => (accessMode = FEDERATED)}
							/>
							<span>
								<span class="block text-[13px] font-semibold"
									>{m.connections_connectionwizard_expose_through_a_federated_engine()}</span
								>
								<span class="block text-[11px] text-slate-600 dark:text-slate-300">
									{m.connections_connectionwizard_bind_this_source_into_an_engine()}
								</span>
							</span>
						</label>
					{/if}
				</fieldset>
			{/if}

			{#if accessMode === FEDERATED}
				{#if compatibleEngines.length > 0}
					<div class="rounded-lg border border-border bg-bg-panel p-3">
						<p class="text-[13px] font-semibold">
							{m.connections_connectionwizard_expose_through_a_federated_engine()}
						</p>
						<p class="mb-2 text-[11px] text-slate-600 dark:text-slate-300">
							{m.connections_connectionwizard_bind_this_source_into_an_engine()}
						</p>
						{#each compatibleEngines as engine (engine.id)}
							<label class="flex items-center gap-2 py-1">
								<input
									type="checkbox"
									class="h-4 w-4 accent-primary"
									checked={bindEngineIds.includes(engine.id)}
									onchange={(e) => toggleEngine(engine.id, e.currentTarget.checked)}
								/>
								<KindIcon kind={engine.kind} family="engine" size={15} />
								<span class="text-[12px]">{engine.name}</span>
							</label>
						{/each}
					</div>
				{:else}
					<p class="text-[11px] text-slate-600 dark:text-slate-300">
						{m.connections_connectionwizard_no_federated_engine_can_bind_this()}
					</p>
				{/if}

				{#if bindEngineIds.length > 0}
					<div class="rounded-lg border border-border bg-bg-panel p-3">
						<p class="text-[13px] font-semibold">
							{m.connections_connectionwizard_catalogs_to_create()}
						</p>
						<p class="mb-2 text-[11px] text-slate-600 dark:text-slate-300">
							{isDatabase
								? m.connections_connectionwizard_each_database_becomes_one_catalog()
								: m.connections_connectionwizard_each_warehouse_or_catalog_name_from_the_so()}
						</p>
						<Input
							label={isDatabase
								? m.connections_connectionwizard_database_name()
								: m.connections_connectionwizard_warehouse_or_catalog_name()}
							bind:value={catalogName}
							placeholder={isDatabase ? 'postgres' : 'warehouse'}
						/>
						{#if catalogOutputs.length > 0}
							<ul class="mt-2 flex flex-col gap-1 text-[11px] text-slate-600 dark:text-slate-300">
								{#each catalogOutputs as output (output.trino_catalog_name)}
									<li class="font-mono">
										{output.trino_catalog_name || output.name}
										<span class="text-slate-400 dark:text-slate-500"> · {output.connector}</span>
									</li>
								{/each}
							</ul>
						{/if}
					</div>
				{/if}
			{/if}
		</div>

		<div class="flex justify-between">
			<Button onclick={() => (step = 2)}>{m.connections_connectionwizard_back()}</Button>
			<Button
				variant="primary"
				disabled={(modes.length === 2 && accessMode === null) ||
					(accessMode === FEDERATED && bindEngineIds.length === 0)}
				onclick={() => (step = 4)}>{m.connections_connectionwizard_continue()}</Button
			>
		</div>
	{:else if step === 4 && provider}
		<header>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">
				{m.connections_connectionwizard_test_and_create()}
			</h1>
			<p class="text-[13px] text-slate-600 dark:text-slate-300">
				{m.connections_connectionwizard_step_4_of_4_the_connection()}
			</p>
		</header>

		<div class="rounded-lg border border-border bg-bg-panel p-4">
			<div class="flex items-center gap-2">
				<Button variant="primary" loading={testing} disabled={testing} onclick={runTest}>
					{testing
						? m.connections_connectionwizard_testing()
						: m.connections_connectionwizard_test_connection()}
				</Button>
				{#if tested}
					<Badge tone="success">{m.connections_connectionwizard_tested()}</Badge>
				{:else if testResult && !testResult.success}
					<Badge tone="danger">{m.connections_connectionwizard_failed()}</Badge>
				{:else}
					<span class="text-[11px] text-slate-600 dark:text-slate-300"
						>{m.connections_connectionwizard_editing_a_field_invalidates_the_test()}</span
					>
				{/if}
			</div>

			{#if testError}
				<div class="mt-3">
					<ErrorPanel error={{ code: 'internal', message: testError }} />
				</div>
			{/if}

			{#if testResult}
				<ul class="mt-3 flex flex-col gap-1.5">
					{#each testResult.checks ?? [] as check (check.name)}
						<li class="flex items-start gap-2 text-[12px]">
							<span
								class="mt-0.5 inline-flex h-4 w-4 items-center justify-center rounded-full text-[9px] font-bold {check.ok
									? 'bg-green-100 text-green-700'
									: 'bg-red-100 text-red-700'}"
							>
								{check.ok ? '✓' : '✕'}
							</span>
							<span class="flex-1">
								{check.name}
								{#if check.skipped}<span class="text-slate-600 dark:text-slate-300">
										{m.connections_connectionwizard_skipped()}</span
									>{/if}
								{#if check.detail}
									<span class="block text-[10px] text-slate-600 dark:text-slate-300"
										>{check.detail}</span
									>
								{/if}
								{#if check.error_code}
									<span class="block font-mono text-[10px] text-danger">{check.error_code}</span>
								{/if}
							</span>
						</li>
					{/each}
				</ul>
				{#if !testResult.success && testResult.error_message}
					<p class="mt-2 text-[11px] text-danger">{testResult.error_message}</p>
				{/if}
			{/if}
		</div>

		{#if createError}
			<div class="rounded-lg border border-danger/30 bg-red-50 p-3">
				<p class="text-[12px] text-danger">{createError}</p>
				{#if created}
					<p class="mt-1 text-[11px] text-slate-600 dark:text-slate-300">
						{m.connections_connectionwizard_the_connection_was_created_finish_the({
							name: created.connection.name
						})}
					</p>
					<Button class="mt-2" size="sm" onclick={finish}
						>{m.connections_connectionwizard_open_connections()}</Button
					>
				{/if}
			</div>
		{/if}

		<div class="flex justify-between">
			<Button onclick={() => (step = 3)} disabled={creating}
				>{m.connections_connectionwizard_back()}</Button
			>
			<div class="flex items-center gap-2">
				{#if !tested && !created}
					<span class="text-[11px] text-slate-600 dark:text-slate-300"
						>{m.connections_connectionwizard_run_the_test_first()}</span
					>
				{/if}
				<Button
					variant="primary"
					loading={creating}
					disabled={!tested || creating || created !== null}
					onclick={create}
				>
					{m.connections_connectionwizard_create()}
				</Button>
			</div>
		</div>
	{:else if step === 5}
		<header>
			<div class="flex items-center gap-2">
				<Badge tone="success">{m.connections_connectionwizard_created()}</Badge>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{created?.connection.name}</h1>
			</div>
			<p class="text-[13px] text-slate-600 dark:text-slate-300">
				{m.connections_connectionwizard_the_connection_is_ready()}
			</p>
		</header>

		<div class="rounded-lg border border-border bg-bg-panel p-4 text-[13px]">
			<p>
				<KindIcon kind={created?.connection.type ?? ''} size={15} />
				<span class="ml-1 font-mono">{created?.connection.type}</span>
			</p>
			{#if created?.engine}
				<p class="mt-2">
					{m.connections_connectionwizard_engine()}
					<span class="font-semibold">{created.engine.name}</span>
					{m.connections_connectionwizard_registered_with_its_native_catalogs()}
				</p>
			{/if}
			{#if created?.catalogs.length}
				<p class="mt-2">
					{m.connections_connectionwizard_catalogs()}
					<span class="font-mono">{created.catalogs.map((c) => c.name).join(', ')}</span>
				</p>
			{/if}
		</div>

		{#if created?.catalogs.length && grantable.length > 0}
			<section class="rounded-lg border border-border bg-bg-panel p-4">
				<h2 class="text-[13px] font-semibold">{m.connections_connectionwizard_grant_access()}</h2>
				<p class="mb-3 text-[11px] text-slate-600 dark:text-slate-300">
					{m.connections_connectionwizard_give_a_workspace_permission_to_query()}
				</p>
				<div class="grid gap-2 sm:grid-cols-2">
					<Select
						label={m.connections_connectionwizard_catalog()}
						bind:value={grantCatalog}
						options={created.catalogs.map((c) => ({ value: c.id ?? '', label: c.name ?? '' }))}
					/>
					<Select
						label={m.connections_connectionwizard_workspace()}
						bind:value={grantWorkspace}
						options={grantable.map((w) => ({ value: w.id, label: w.name }))}
					/>
				</div>
				<div class="mt-3 flex items-center gap-2">
					<Button variant="primary" loading={granting} disabled={granting} onclick={() => grant()}>
						{m.connections_connectionwizard_grant()}
					</Button>
					{#if grants.length > 0}
						<span class="text-[11px] text-slate-600 dark:text-slate-300"
							>{plural(
								grants.length,
								m.connections_connectionwizard_grants_added_one,
								m.connections_connectionwizard_grants_added_other
							)}</span
						>
					{/if}
				</div>
				{#if grantError}
					<div class="mt-3">
						<ErrorPanel error={grantError} />
					</div>
				{/if}
				{#if grants.length > 0}
					<ul class="mt-3 flex flex-col gap-1 text-[11px] text-slate-600 dark:text-slate-300">
						{#each grants as item (item)}
							<li>{item}</li>
						{/each}
					</ul>
				{/if}
			</section>
		{/if}

		<div class="flex justify-end">
			<Button variant="primary" onclick={finish}>{m.connections_connectionwizard_done()}</Button>
		</div>
	{/if}
</div>
