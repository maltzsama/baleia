<script lang="ts">
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { SvelteURLSearchParams } from 'svelte/reactivity';
	import { apiErrorMessage } from '$lib/api/errors';
	import { can, canGlobal } from '$lib/authz/can';
	import type { CatalogSummary } from '$lib/catalogs/admin';
	import AttributeFreshness from '$lib/governance/AttributeFreshness.svelte';
	import EngineStates from '$lib/governance/EngineStates.svelte';
	import GuaranteeNotice from '$lib/governance/GuaranteeNotice.svelte';
	import PolicyEditor from '$lib/governance/PolicyEditor.svelte';
	import WritesSummary from '$lib/governance/WritesSummary.svelte';
	import {
		DataPolicyError,
		deletePolicy,
		getStatus,
		listCatalogPolicies,
		listDefinerViews,
		listManageableCatalogs,
		policyDialect,
		publishCatalog,
		type CatalogDataPolicies,
		type DataPolicy,
		type DataPolicyDefinerView,
		type DataPolicyProblem,
		type DataPolicyStatus
	} from '$lib/governance/api';
	import {
		emptyDirectory,
		loadDirectory,
		subjectSummary,
		type Directory
	} from '$lib/governance/directory';
	import { invalidReasonLabel, problemLabel } from '$lib/governance/labels';
	import { combineWrites, groupByTable, subjectKey } from '$lib/governance/policies';
	import { m } from '$lib/paraglide/messages';
	import { authStore } from '$lib/stores/auth.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import CodeBlock from '$lib/ui/patterns/CodeBlock.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	// Catalogs whose policies the caller manages: their connection's owner or
	// a global admin (FD9, `manage_data_policies` on the catalog).
	let catalogs = $state.raw<CatalogSummary[]>([]);
	let catalogId = $state(page.url.searchParams.get('catalog') ?? '');
	let loadingCatalogs = $state(true);
	let catalogsError = $state('');

	let data = $state.raw<CatalogDataPolicies | null>(null);
	let views = $state.raw<DataPolicyDefinerView[]>([]);
	// idle: no lookup is made (no policy, or policies off).
	let viewsState = $state<'idle' | 'loading' | 'ready' | 'error'>('idle');
	let viewsError = $state('');
	let status = $state.raw<DataPolicyStatus | null>(null);
	let statusLoading = $state(false);
	let statusError = $state('');
	let directory = $state.raw<Directory>(emptyDirectory);
	let dialect = $state<string | null>(null);
	let loading = $state(false);
	let error = $state('');
	// Each catalog load takes a token: a response for an earlier selection,
	// arriving late, changes nothing.
	let catalogRequest = 0;

	let editorOpen = $state(false);
	let editorSession = $state(0);
	let editing = $state.raw<DataPolicy | null>(null);
	let editingTable = $state.raw<{ schema: string; table: string } | null>(null);
	let deleting = $state.raw<DataPolicy | null>(null);
	let deleteOpen = $state(false);
	let publishOpen = $state(false);
	let problems = $state.raw<DataPolicyProblem[]>([]);

	const isAdmin = $derived(canGlobal(authStore.user, 'manage_data_policies'));
	const catalog = $derived(catalogs.find((c) => c.id === catalogId) ?? null);
	const canManage = $derived(can(catalog, 'manage_data_policies'));
	const tables = $derived(groupByTable(data?.policies ?? []));
	const policyNames = $derived(new Map((data?.policies ?? []).map((p) => [p.id, p.name])));

	async function loadCatalogs() {
		loadingCatalogs = true;
		catalogsError = '';
		try {
			catalogs = await listManageableCatalogs();
			if (!catalogs.some((c) => c.id === catalogId)) catalogId = catalogs[0]?.id ?? '';
		} catch (e) {
			catalogsError = apiErrorMessage(e);
		} finally {
			loadingCatalogs = false;
		}
		if (catalogId) await loadCatalog();
	}

	async function loadCatalog() {
		if (!catalogId) return;
		const id = catalogId;
		const token = ++catalogRequest;
		const current = () => token === catalogRequest;
		loading = true;
		error = '';
		problems = [];
		views = [];
		viewsState = 'idle';
		viewsError = '';
		try {
			const [policies, dir, base] = await Promise.all([
				listCatalogPolicies(id),
				loadDirectory(),
				policyDialect(catalogs.find((c) => c.id === id) ?? null)
			]);
			if (!current()) return;
			data = policies;
			directory = dir;
			dialect = base;
		} catch (e) {
			if (!current()) return;
			error = apiErrorMessage(e instanceof DataPolicyError ? e.body : e);
			data = null;
		} finally {
			if (current()) loading = false;
		}
		await loadViews(id, current);
	}

	// Each engine's revision is a global admin's (FD9); a failure shows with a
	// retry and leaves the policies on screen.
	async function loadStatus() {
		statusLoading = true;
		statusError = '';
		try {
			status = await getStatus();
		} catch (e) {
			statusError = apiErrorMessage(e instanceof DataPolicyError ? e.body : e);
		} finally {
			statusLoading = false;
		}
	}

	// /auth/me may arrive after the page mounts: read the status once the
	// caller is known to be an admin.
	let statusStarted = false;
	$effect(() => {
		if (isAdmin && !statusStarted) {
			statusStarted = true;
			void loadStatus();
		}
	});

	// The DEFINER views need the catalog manager; a failure there does not hide
	// the policies.
	async function loadViews(id: string, current: () => boolean) {
		if (!current() || !data?.enabled || data.policies.length === 0) return;
		viewsState = 'loading';
		try {
			const found = await listDefinerViews(id);
			if (!current()) return;
			views = found;
			viewsState = 'ready';
		} catch (e) {
			if (!current()) return;
			viewsError = apiErrorMessage(e instanceof DataPolicyError ? e.body : e);
			viewsState = 'error';
		}
	}

	function selectCatalog() {
		const params = new SvelteURLSearchParams(page.url.searchParams);
		params.set('catalog', catalogId);
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(`${page.url.pathname}?${params}`, {
			replaceState: true,
			keepFocus: true,
			noScroll: true
		});
		void loadCatalog();
	}

	function addPolicy(table: { schema: string; table: string } | null) {
		editing = null;
		editingTable = table;
		editorSession += 1;
		editorOpen = true;
	}

	function editPolicy(policy: DataPolicy) {
		editing = policy;
		editingTable = null;
		editorSession += 1;
		editorOpen = true;
	}

	function askDelete(policy: DataPolicy) {
		deleting = policy;
		deleteOpen = true;
	}

	async function confirmDelete() {
		if (!deleting) return;
		try {
			await deletePolicy(deleting.id);
		} catch (e) {
			throw e instanceof DataPolicyError ? e.body : e;
		}
		toasts.success(m.governance_policy_deleted({ name: deleting.name }));
		await loadCatalog();
	}

	async function confirmPublish() {
		problems = [];
		try {
			const revision = await publishCatalog(catalogId);
			toasts.success(m.governance_published({ number: String(revision.number) }));
			if (isAdmin) void loadStatus();
		} catch (e) {
			if (e instanceof DataPolicyError) {
				problems = e.problems;
				throw e.body;
			}
			throw e;
		}
		await loadCatalog();
	}

	// DEFINER views that read a table, matched on the engine-side name.
	function viewsOver(table: string): DataPolicyDefinerView[] {
		return views.filter((view) =>
			view.reads.some((read) => read === table || read.endsWith(`.${table}`))
		);
	}

	onMount(loadCatalogs);
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.governance_by_object_title()}</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{m.governance_by_object_description()}
			</p>
		</div>
	</div>

	<div class="mb-4"><GuaranteeNotice /></div>

	{#if loadingCatalogs}
		<div class="flex flex-col gap-2" role="status" aria-label={m.governance_loading()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if catalogsError}
		<ErrorState
			title={m.governance_could_not_load_catalogs()}
			message={catalogsError}
			onretry={loadCatalogs}
		/>
	{:else if catalogs.length === 0}
		<EmptyState
			title={m.governance_no_catalogs_title()}
			description={m.governance_no_catalogs_description()}
		/>
	{:else}
		<div class="mb-4 flex flex-wrap items-end gap-3">
			<Select
				label={m.common_catalog()}
				bind:value={catalogId}
				options={catalogs.map((c) => ({ value: c.id ?? '', label: c.name ?? c.id ?? '' }))}
				onchange={selectCatalog}
			/>
			{#if data?.enabled}
				{#if data.draft}
					<Badge tone="warning">{m.governance_unpublished_changes()}</Badge>
				{:else}
					<Badge tone="success">{m.governance_up_to_date()}</Badge>
				{/if}
				{#if canManage}
					<Button onclick={() => addPolicy(null)}>
						<PlusIcon class="h-4 w-4" aria-hidden="true" />
						{m.governance_add_policy()}
					</Button>
					<Button variant="primary" disabled={!data.draft} onclick={() => (publishOpen = true)}>
						{m.governance_publish()}
					</Button>
				{/if}
			{/if}
		</div>

		{#if problems.length > 0}
			<section
				aria-labelledby="governance-problems"
				class="mb-4 rounded-lg border border-red-300 bg-red-50 p-3 text-[12px] text-red-800 dark:border-red-800 dark:bg-red-950 dark:text-red-200"
			>
				<h2 id="governance-problems" class="mb-1 text-[13px] font-semibold">
					{m.governance_publish_refused()}
				</h2>
				<ul class="list-disc pl-5">
					{#each problems as problem, index (`${problem.code}-${problem.policy_id}-${problem.detail}-${index}`)}
						<li>
							{problemLabel(
								problem,
								problem.policy_id ? policyNames.get(problem.policy_id) : undefined
							)}
						</li>
					{/each}
				</ul>
			</section>
		{/if}

		{#if loading}
			<div class="flex flex-col gap-2" role="status" aria-label={m.governance_loading()}>
				{#each [0, 1] as item (item)}
					<Skeleton class="h-24 w-full" />
				{/each}
			</div>
		{:else if error}
			<ErrorState
				title={m.governance_could_not_load_policies()}
				message={error}
				onretry={loadCatalog}
			/>
		{:else if data && !data.enabled}
			<EmptyState
				title={m.governance_disabled_title()}
				description={m.governance_disabled_description()}
			/>
		{:else if data}
			{#if data.names.length > 0}
				<p class="mb-3 text-[12px] text-text-muted">
					{m.governance_engine_names({ names: data.names.join(', ') })}
				</p>
			{/if}
			{#if isAdmin}
				<div class="mb-4">
					{#if statusLoading}
						<div role="status" aria-label={m.governance_loading()}>
							<Skeleton class="h-16 w-full" />
						</div>
					{:else if statusError}
						<ErrorState
							title={m.governance_could_not_load_status()}
							message={statusError}
							onretry={loadStatus}
						/>
					{:else if status}
						<EngineStates {status} />
					{/if}
				</div>
			{/if}

			{#if tables.length === 0}
				<EmptyState
					title={m.governance_no_policies_title()}
					description={m.governance_no_policies_description()}
				>
					{#if canManage}
						<Button variant="primary" onclick={() => addPolicy(null)}>
							{m.governance_add_policy()}
						</Button>
					{/if}
				</EmptyState>
			{:else}
				<div class="flex flex-col gap-4">
					{#each tables as group (group.key)}
						{@const table = group.label}
						{@const policies = group.policies}
						{@const writes = combineWrites(policies.filter((p) => p.state === 'enabled'))}
						{@const tableViews = viewsOver(table)}
						<section
							aria-label={m.governance_table_section({ table })}
							class="rounded-lg border border-border bg-bg-panel"
						>
							<header
								class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2"
							>
								<h2 class="font-mono text-[13px] font-semibold">{table}</h2>
								{#if canManage}
									<Button
										size="sm"
										onclick={() => addPolicy({ schema: group.schema, table: group.table })}
									>
										<PlusIcon class="h-3.5 w-3.5" aria-hidden="true" />
										{m.governance_add_policy_to({ table })}
									</Button>
								{/if}
							</header>
							<div class="overflow-x-auto">
								<table class="w-full text-left text-[12px]">
									<thead class="text-[10px] uppercase tracking-wide text-text-muted">
										<tr>
											<th class="px-3 py-2">{m.common_name()}</th>
											<th class="px-3 py-2">{m.common_kind()}</th>
											<th class="px-3 py-2">{m.governance_subjects()}</th>
											<th class="px-3 py-2">{m.governance_expression()}</th>
											<th class="px-3 py-2">{m.governance_validation()}</th>
											<th class="px-3 py-2"><span class="sr-only">{m.common_action()}</span></th>
										</tr>
									</thead>
									<tbody>
										{#each policies as policy (policy.id)}
											<tr class="border-t border-border/60 align-top">
												<td class="px-3 py-2">
													<div class="font-medium">{policy.name}</div>
													{#if policy.state === 'disabled'}
														<Badge tone="neutral">{m.governance_state_disabled()}</Badge>
													{/if}
													{#if policy.description}
														<div class="text-text-muted">{policy.description}</div>
													{/if}
												</td>
												<td class="px-3 py-2">
													{#if policy.kind === 'row_filter'}
														<div>{m.governance_kind_row_filter()}</div>
														<div class="text-text-muted">
															{policy.mode === 'restrictive'
																? m.governance_mode_restrictive()
																: m.governance_mode_permissive()}
														</div>
													{:else}
														<div>{m.governance_mask_on({ column: policy.column ?? '' })}</div>
														<div class="text-text-muted">
															{m.governance_priority_value({ priority: String(policy.priority) })}
														</div>
													{/if}
												</td>
												<td class="px-3 py-2">
													<ul>
														{#each policy.subjects as subject (subjectKey(subject))}
															<li>{subjectSummary(subject, directory)}</li>
														{/each}
													</ul>
												</td>
												<td class="min-w-48 px-3 py-2">
													<CodeBlock
														code={policy.expression}
														language="sql"
														{dialect}
														label={m.governance_expression_of({ name: policy.name })}
														maxHeight="120px"
													/>
												</td>
												<td class="px-3 py-2">
													{#if policy.validation === 'valid'}
														<Badge tone="success">{m.governance_valid()}</Badge>
													{:else if policy.validation === 'invalid'}
														<Badge tone="danger">{m.governance_invalid()}</Badge>
														<div class="mt-1">{invalidReasonLabel(policy.invalid_reason)}</div>
														{#if policy.invalid_detail}
															<code class="font-mono text-[11px]">{policy.invalid_detail}</code>
														{/if}
													{:else}
														<Badge tone="neutral">{m.governance_pending()}</Badge>
													{/if}
												</td>
												<td class="px-3 py-2">
													<div class="flex justify-end gap-1">
														{#if can(policy, 'update')}
															<Button
																size="sm"
																variant="ghost"
																ariaLabel={m.governance_edit_policy({ name: policy.name })}
																onclick={() => editPolicy(policy)}
															>
																<PencilIcon class="h-3.5 w-3.5" aria-hidden="true" />
															</Button>
														{/if}
														{#if can(policy, 'delete')}
															<Button
																size="sm"
																variant="ghost"
																ariaLabel={m.governance_delete_policy({ name: policy.name })}
																onclick={() => askDelete(policy)}
															>
																<Trash2Icon class="h-3.5 w-3.5" aria-hidden="true" />
															</Button>
														{/if}
													</div>
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
							<div class="grid gap-3 border-t border-border px-3 py-2 md:grid-cols-2">
								<div>
									<h3 class="mb-1 text-[12px] font-semibold">{m.governance_writes_title()}</h3>
									{#if writes}
										<WritesSummary {writes} />
									{:else}
										<p class="text-[11px] text-text-muted">{m.governance_no_enabled_policy()}</p>
									{/if}
								</div>
								<div>
									<h3 class="mb-1 text-[12px] font-semibold">{m.governance_definer_views()}</h3>
									{#if viewsState === 'loading'}
										<div role="status" aria-label={m.governance_loading()}>
											<Skeleton class="h-4 w-40" />
										</div>
									{:else if viewsState === 'error'}
										<p class="text-[11px] text-danger">{viewsError}</p>
									{:else if tableViews.length === 0}
										<p class="text-[11px] text-text-muted">{m.governance_no_definer_views()}</p>
									{:else}
										<ul class="flex flex-col gap-1 text-[11px]">
											{#each tableViews as view (view.view)}
												<li>
													<code class="font-mono">{view.view}</code>
													{#if view.trusted}
														<Badge tone="danger">{m.governance_view_trusted()}</Badge>
													{:else}
														<span class="text-text-muted"
															>{m.governance_view_applies_owner({ owner: view.owner })}</span
														>
													{/if}
													{#if view.unreadable}
														<Badge tone="warning">{m.governance_view_unreadable()}</Badge>
													{/if}
												</li>
											{/each}
										</ul>
									{/if}
									<p class="mt-1 text-[11px] text-text-muted">{m.governance_definer_help()}</p>
								</div>
							</div>
						</section>
					{/each}
				</div>
			{/if}

			<div class="mt-4"><AttributeFreshness /></div>
		{/if}
	{/if}
</div>

{#if catalogId}
	<!-- A fresh form on every opening. -->
	{#key editorSession}
		<PolicyEditor
			bind:open={editorOpen}
			{catalogId}
			policy={editing}
			table={editingTable}
			{directory}
			onsaved={(saved) => {
				toasts.success(m.governance_policy_saved({ name: saved.name }));
				void loadCatalog();
			}}
		/>
	{/key}
{/if}

{#if deleting}
	<ConfirmDialog
		bind:open={deleteOpen}
		title={m.governance_delete_policy({ name: deleting.name })}
		resourceName={deleting.name}
		confirmLabel={m.common_delete()}
		consequences={[
			m.governance_delete_consequence_draft(),
			m.governance_delete_consequence_publish()
		]}
		onconfirm={confirmDelete}
	/>
{/if}

<ConfirmDialog
	bind:open={publishOpen}
	title={m.governance_publish_title({ catalog: catalog?.name ?? '' })}
	resourceName={catalog?.name ?? ''}
	confirmLabel={m.governance_publish()}
	danger={false}
	consequences={[
		m.governance_publish_consequence_engines(),
		m.governance_publish_consequence_writes(),
		m.governance_publish_consequence_lease()
	]}
	onconfirm={confirmPublish}
/>
