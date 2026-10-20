<script lang="ts">
	import { m } from '$lib/paraglide/messages';
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
		createCatalog,
		createConnection,
		createEngine,
		grantCatalogAccess,
		listEngines,
		listGrantableWorkspaces,
		loadProviders,
		testDraft,
		type BindingSupport,
		type Catalog,
		type CatalogConnection,
		type Engine,
		type TestResult,
		type Workspace
	} from './api';
	import ProviderForm from './ProviderForm.svelte';
	import {
		canQueryDirectly,
		databaseName,
		familyDescription,
		familyLabel,
		familyOrder,
		initialValues,
		intentsFor,
		missingFields,
		normalizeCatalogName,
		normalizeEngineName,
		readsMetadata,
		roleLabel,
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
	let direct = $state(false);
	let bindEngineIds = $state<string[]>([]);
	let catalogNames = $state<string[]>(['']);
	let connector = $state('');

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
	let grantPermission = $state<'read' | 'write'>('read');
	let granting = $state(false);
	let grantError = $state<ConnectionRequestError | null>(null);
	let grants = $state<string[]>([]);

	onMount(async () => {
		try {
			const [providerList, engineList] = await Promise.all([loadProviders(), listEngines()]);
			providers = providerList;
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

	const groups = $derived(
		familyOrder()
			.map((family) => ({
				family,
				label: familyLabel(family),
				description: familyDescription(family),
				providers: providers.filter((p) => p.family === family)
			}))
			.filter((group) => group.providers.length > 0)
	);

	const compatibleEngines = $derived(
		provider ? engines.filter((engine) => Boolean(support[engine.id]?.[provider!.kind])) : []
	);

	const selectedSupport = $derived(
		provider && bindEngineIds.length > 0 ? support[bindEngineIds[0]]?.[provider.kind] : undefined
	);

	const connectors = $derived(selectedSupport?.connectors ?? []);
	const reservedNames = $derived(selectedSupport?.reserved_names ?? []);
	const isDatabase = $derived(provider?.family === 'database');
	const missing = $derived(provider ? missingFields(provider, values, secrets) : []);
	const snapshot = $derived(
		JSON.stringify({
			name,
			values,
			secrets,
			credentialsReadOnly,
			direct,
			bindEngineIds,
			catalogNames,
			connector
		})
	);
	const tested = $derived(testedSnapshot !== '' && testedSnapshot === snapshot);

	$effect(() => {
		if (connectors.length > 0 && !connectors.includes(connector)) {
			connector = connectors[0];
		}
	});

	function choose(next: ConnectionProvider) {
		if (next.status.state === 'unsupported') return;
		provider = next;
		name = '';
		values = initialValues(next);
		secrets = {};
		credentialsReadOnly = false;
		direct = false;
		bindEngineIds = [];
		catalogNames = [''];
		connector = '';
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
				catalogNames = [db];
				name = name || db;
			}
		}
		if (catalogNames.length === 0) catalogNames = [''];
		step = 3;
	}

	function toggleEngine(engineId: string, checked: boolean) {
		if (checked) {
			bindEngineIds = [...bindEngineIds, engineId];
		} else {
			bindEngineIds = bindEngineIds.filter((id) => id !== engineId);
		}
	}

	function addCatalog() {
		catalogNames = [...catalogNames, ''];
	}

	function setCatalog(index: number, value: string) {
		catalogNames = catalogNames.map((item, i) => (i === index ? value : item));
	}

	function removeCatalog(index: number) {
		catalogNames = catalogNames.filter((_, i) => i !== index);
	}

	function usage(): Usage {
		return { direct, bindEngineIds };
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
				default_readonly: true,
				credentials_read_only: credentialsReadOnly,
				intents: intentsFor(provider, usage())
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
		try {
			const connection = await createConnection({
				name,
				type: provider.kind,
				config: values,
				credentials: secrets,
				default_readonly: true,
				credentials_read_only: credentialsReadOnly
			});
			let engine: Engine | undefined;
			const catalogs: Catalog[] = [];
			created = { connection, engine, catalogs };
			if (direct) {
				const engineName = normalizeEngineName(name);
				if (!engineName) throw new Error('The connection name cannot be used as an engine name.');
				engine = await createEngine({ name: engineName, connection_id: connection.id });
				created = { connection, engine, catalogs };
			}
			if (bindEngineIds.length > 0) {
				const names = [...new Set(catalogNames.map((item) => item.trim()).filter(Boolean))];
				if (names.length === 0) throw new Error('Choose at least one catalog to create.');
				for (const raw of names) {
					const catalogName = normalizeCatalogName(raw);
					if (!catalogName) throw new Error(`“${raw}” cannot be used as a catalog name.`);
					if (reservedNames.includes(catalogName)) {
						throw new Error(`“${catalogName}” is reserved by the engine. Choose another name.`);
					}
					catalogs.push(
						await createCatalog(connection.id, {
							name: raw,
							trino_catalog_name: catalogName,
							warehouse: raw,
							connector: connector || connectors[0] || '',
							description: ''
						})
					);
				}
				created = { connection, engine, catalogs };
			}
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

	async function grant(permission: 'read' | 'write') {
		if (!grantCatalog || !grantWorkspace) return;
		granting = true;
		grantError = null;
		try {
			await grantCatalogAccess({
				catalogId: grantCatalog,
				workspaceId: grantWorkspace,
				permission
			});
			const catalog = created?.catalogs.find((item) => item.id === grantCatalog);
			const workspace = grantable.find((item) => item.id === grantWorkspace);
			grants = [
				...grants,
				`${workspace?.name ?? 'Workspace'} can ${permission} ${catalog?.name ?? m.connections_connectionwizard_the_catalog()}`
			];
			grantPermission = permission;
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
		if (p.status.state === 'preview') return m.identity_identityproviderform_preview();
		if (p.status.state === 'unsupported') return 'Unsupported';
		return '';
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
			<h1 class="text-xl font-semibold tracking-[-0.3px]">New connection</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				Choose what you are connecting. Baleia reads the rest from the provider.
			</p>
		</header>

		{#each groups as group (group.family)}
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
						{@const disabled = p.status.state === 'unsupported'}
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
									<Badge tone={p.status.state === 'preview' ? 'warning' : 'neutral'}>
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
					Step 2 of 4 — connection settings
				</p>
			</div>
		</header>

		<div class="rounded-lg border border-border bg-bg-panel p-4">
			<Input label="Connection name *" bind:value={name} placeholder="e.g. analytics-prod" />
			<div class="mt-3">
				<ProviderForm
					{provider}
					bind:values
					bind:secrets
					allowReferences={compatibleEngines.length > 0}
				/>
			</div>
			<div class="mt-3 rounded-md border border-border bg-bg-toolbar px-3 py-2.5">
				<Checkbox label="These credentials cannot modify data" bind:checked={credentialsReadOnly} />
				<p class="mt-1 text-[10px] leading-relaxed text-slate-600 dark:text-slate-300">
					Assert this when the account is read-only at the source. It unlocks read grants and agents
					on engines that cannot enforce read-only sessions.
				</p>
			</div>
		</div>

		{#if missing.length > 0}
			<p class="text-[11px] text-slate-600 dark:text-slate-300">
				Fill the required fields to continue.
			</p>
		{/if}

		<div class="flex justify-between">
			<Button onclick={() => (step = 1)}>Back</Button>
			<Button variant="primary" disabled={!name.trim() || missing.length > 0} onclick={toUsage}>
				Continue
			</Button>
		</div>
	{:else if step === 3 && provider}
		<header>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">How will you use it?</h1>
			<p class="text-[13px] text-slate-600 dark:text-slate-300">
				Step 3 of 4 — Baleia can read metadata, run SQL directly, or expose the source through a
				federated engine.
			</p>
		</header>

		<div class="flex flex-col gap-3">
			{#if readsMetadata(provider)}
				<div class="flex items-start gap-2 rounded-lg border border-border bg-bg-panel p-3">
					<Badge tone="success">Always on</Badge>
					<p class="text-[12px] text-slate-600 dark:text-slate-300">
						Baleia lists the catalogs, schemas and tables this connection exposes.
					</p>
				</div>
			{/if}

			{#if canQueryDirectly(provider)}
				<label class="flex items-start gap-2 rounded-lg border border-border bg-bg-panel p-3">
					<input type="checkbox" class="mt-0.5 h-4 w-4 accent-primary" bind:checked={direct} />
					<span>
						<span class="block text-[13px] font-semibold">Query it directly</span>
						<span class="block text-[11px] text-slate-600 dark:text-slate-300">
							Register it as a query engine and run SQL on it without Trino.
						</span>
					</span>
				</label>
			{/if}

			{#if compatibleEngines.length > 0}
				<div class="rounded-lg border border-border bg-bg-panel p-3">
					<p class="text-[13px] font-semibold">Expose through a federated engine</p>
					<p class="mb-2 text-[11px] text-slate-600 dark:text-slate-300">
						Bind this source into an engine so its catalogs can be queried next to the others.
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
					No federated engine can bind this source right now.
				</p>
			{/if}

			{#if bindEngineIds.length > 0}
				<div class="rounded-lg border border-border bg-bg-panel p-3">
					<p class="text-[13px] font-semibold">Catalogs to create</p>
					<p class="mb-2 text-[11px] text-slate-600 dark:text-slate-300">
						{isDatabase
							? m.connections_connectionwizard_each_database_becomes_one_catalog()
							: m.connections_connectionwizard_each_warehouse_or_catalog_name_from_the_so()}
					</p>
					{#each catalogNames as catalogName, index (index)}
						<div class="mb-2 flex items-end gap-2">
							<div class="flex-1">
								<Input
									label={isDatabase
										? m.connections_connectionwizard_database_name()
										: m.connections_connectionwizard_warehouse_or_catalog_name()}
									value={catalogName}
									placeholder={isDatabase ? 'postgres' : 'warehouse'}
									oninput={(e) => setCatalog(index, (e.target as HTMLInputElement).value)}
								/>
							</div>
							{#if catalogNames.length > 1}
								<Button variant="ghost" size="sm" onclick={() => removeCatalog(index)}
									>Remove</Button
								>
							{/if}
						</div>
					{/each}
					{#if !isDatabase}
						<Button size="sm" onclick={addCatalog}>Add catalog</Button>
					{/if}
					{#if connectors.length > 1}
						<div class="mt-2">
							<Select
								label="Connector"
								bind:value={connector}
								options={connectors.map((item) => ({ value: item, label: item }))}
							/>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<div class="flex justify-between">
			<Button onclick={() => (step = 2)}>Back</Button>
			<Button variant="primary" onclick={() => (step = 4)}>Continue</Button>
		</div>
	{:else if step === 4 && provider}
		<header>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">Test and create</h1>
			<p class="text-[13px] text-slate-600 dark:text-slate-300">
				Step 4 of 4 — the connection is saved only after the checks pass.
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
					<Badge tone="success">Tested</Badge>
				{:else if testResult && !testResult.success}
					<Badge tone="danger">Failed</Badge>
				{:else}
					<span class="text-[11px] text-slate-600 dark:text-slate-300"
						>Editing a field invalidates the test.</span
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
										(skipped)</span
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
						The connection {created.connection.name} was created. Finish the remaining steps from the
						connections page.
					</p>
					<Button class="mt-2" size="sm" onclick={finish}>Open connections</Button>
				{/if}
			</div>
		{/if}

		<div class="flex justify-between">
			<Button onclick={() => (step = 3)} disabled={creating}>Back</Button>
			<div class="flex items-center gap-2">
				{#if !tested && !created}
					<span class="text-[11px] text-slate-600 dark:text-slate-300">Run the test first.</span>
				{/if}
				<Button
					variant="primary"
					loading={creating}
					disabled={!tested || creating || created !== null}
					onclick={create}
				>
					Create
				</Button>
			</div>
		</div>
	{:else if step === 5}
		<header>
			<div class="flex items-center gap-2">
				<Badge tone="success">Created</Badge>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{created?.connection.name}</h1>
			</div>
			<p class="text-[13px] text-slate-600 dark:text-slate-300">The connection is ready.</p>
		</header>

		<div class="rounded-lg border border-border bg-bg-panel p-4 text-[13px]">
			<p>
				<KindIcon kind={created?.connection.type ?? ''} size={15} />
				<span class="ml-1 font-mono">{created?.connection.type}</span>
			</p>
			{#if created?.engine}
				<p class="mt-2">
					Engine <span class="font-semibold">{created.engine.name}</span> registered with its native catalogs.
				</p>
			{/if}
			{#if created?.catalogs.length}
				<p class="mt-2">
					Catalogs: <span class="font-mono">{created.catalogs.map((c) => c.name).join(', ')}</span>
				</p>
			{/if}
		</div>

		{#if created?.catalogs.length && grantable.length > 0}
			<section class="rounded-lg border border-border bg-bg-panel p-4">
				<h2 class="text-[13px] font-semibold">Grant access</h2>
				<p class="mb-3 text-[11px] text-slate-600 dark:text-slate-300">
					Give a workspace permission to query these catalogs.
				</p>
				<div class="grid gap-2 sm:grid-cols-3">
					<Select
						label="Catalog"
						bind:value={grantCatalog}
						options={created.catalogs.map((c) => ({ value: c.id ?? '', label: c.name ?? '' }))}
					/>
					<Select
						label="Workspace"
						bind:value={grantWorkspace}
						options={grantable.map((w) => ({ value: w.id, label: w.name }))}
					/>
					<Select
						label="Permission"
						bind:value={grantPermission}
						options={[
							{ value: 'read', label: 'Read' },
							{ value: 'write', label: 'Write' }
						]}
					/>
				</div>
				<div class="mt-3 flex items-center gap-2">
					<Button
						variant="primary"
						loading={granting}
						disabled={granting}
						onclick={() => grant(grantPermission)}
					>
						Grant
					</Button>
					{#if grants.length > 0}
						<span class="text-[11px] text-slate-600 dark:text-slate-300"
							>{grants.length} grant(s) added</span
						>
					{/if}
				</div>
				{#if grantError}
					<div class="mt-3">
						<ErrorPanel error={grantError} />
						{#if grantError.code === 'read_only_unenforceable'}
							<Button class="mt-2" size="sm" onclick={() => grant('write')} disabled={granting}>
								Grant write instead
							</Button>
						{/if}
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
			<Button variant="primary" onclick={finish}>Done</Button>
		</div>
	{/if}
</div>
