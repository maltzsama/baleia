<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { client } from '$lib/api/client';
	import ActivityPanel from '$lib/audit/ActivityPanel.svelte';
	import { workspaceStore, type Workspace } from '$lib/stores/workspace.svelte';
	import type { components } from '$lib/api/schema';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	type Tab = 'general' | 'target' | 'catalogs' | 'members' | 'activity';
	type Target = components['schemas']['WorkspaceTarget'];
	type Access = components['schemas']['WorkspaceCatalogAccess'];
	type Impact = components['schemas']['WorkspaceImpact'];
	type TeamMember = components['schemas']['TeamMember'];
	type Catalog = components['schemas']['CatalogSummary'];

	const workspaceId = $derived(page.params.workspace_id ?? '');

	let tab = $state<Tab>('general');
	let ws = $state<Workspace | null>(null);
	let loading = $state(true);
	let error = $state('');
	let targets = $state<Target[]>([]);
	let grants = $state<Access[]>([]);
	let impact = $state<Impact | null>(null);
	let members = $state<TeamMember[]>([]);

	let name = $state('');
	let description = $state('');
	let saving = $state(false);
	let editError = $state('');

	let targetBindingId = $state('');
	let targetSchema = $state('');
	let targetSaving = $state(false);
	let targetError = $state('');

	let grantOpen = $state(false);
	let grantCatalogs = $state<Catalog[]>([]);
	let grantCatalogId = $state('');
	let grantPermission = $state<'read' | 'write'>('read');
	let granting = $state(false);
	let grantError = $state('');

	let deleteOpen = $state(false);
	let revokeOpen = $state(false);
	let revokeCatalog = $state<Access | null>(null);
	let revokeImpact = $state<Impact | null>(null);

	const canEdit = $derived(!!ws?.allowed_actions?.includes('update'));
	const canDelete = $derived(!!ws?.allowed_actions?.includes('delete'));
	const currentTarget = $derived(targets.find((target) => target.is_default));

	async function load() {
		if (!workspaceId) return;
		loading = true;
		error = '';
		try {
			await workspaceStore.load();
			ws = workspaceStore.workspaces.find((item) => item.id === workspaceId) ?? null;
			if (!ws) {
				const { data } = await client.GET('/workspaces/{id}', {
					params: { path: { id: workspaceId } }
				});
				ws = data ?? null;
			}
			if (!ws) {
				error = m.settings_workspaces_workspace_not_found();
				return;
			}
			name = ws.name;
			description = ws.description ?? '';
			const [targetRes, grantRes, impactRes] = await Promise.all([
				client.GET('/workspaces/{id}/targets', { params: { path: { id: workspaceId } } }),
				client.GET('/workspaces/{id}/catalog-access', { params: { path: { id: workspaceId } } }),
				client.GET('/workspaces/{id}/impact', { params: { path: { id: workspaceId } } })
			]);
			targets = targetRes.data ?? [];
			grants = grantRes.data ?? [];
			impact = impactRes.data ?? null;
			targetBindingId = currentTarget?.binding_id ?? '';
			targetSchema = currentTarget ? '' : (ws.default_schema ?? '');
			if (ws.owner_team_id) {
				const { data } = await client.GET('/teams/{id}/members', {
					params: { path: { id: ws.owner_team_id } }
				});
				members = data ?? [];
			}
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

	async function saveGeneral() {
		if (!ws) return;
		saving = true;
		editError = '';
		try {
			const { data, error: err } = await client.PATCH('/workspaces/{id}', {
				params: { path: { id: ws.id } },
				body: { name: name.trim(), description: description.trim() || undefined }
			});
			if (err) throw new Error('Could not save the workspace');
			if (data) ws = data;
			await workspaceStore.load();
		} catch (e) {
			editError = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}

	async function saveTarget() {
		if (!ws) return;
		targetSaving = true;
		targetError = '';
		try {
			const { data, error: err } = await client.PATCH('/workspaces/{id}', {
				params: { path: { id: ws.id } },
				body: {
					default_target: {
						binding_id: targetBindingId || null,
						schema: targetSchema.trim() || null
					}
				}
			});
			if (err) throw new Error('Could not set the default target');
			if (data) ws = data;
			await load();
		} catch (e) {
			targetError = e instanceof Error ? e.message : String(e);
		} finally {
			targetSaving = false;
		}
	}

	async function openGrant() {
		grantError = '';
		grantCatalogId = '';
		grantOpen = true;
		const { data } = await client.GET('/catalogs', {
			params: { query: { action: 'grant', limit: 200 } }
		});
		const granted = new Set(grants.map((grant) => grant.catalog_id));
		grantCatalogs = (data ?? []).filter((catalog) => catalog.id && !granted.has(catalog.id));
	}

	async function submitGrant() {
		if (!ws || !grantCatalogId) return;
		granting = true;
		grantError = '';
		try {
			const { error: err } = await client.POST('/workspaces/{id}/catalog-access', {
				params: { path: { id: ws.id } },
				body: { catalog_id: grantCatalogId, permission: grantPermission }
			});
			if (err) throw new Error('Could not grant access');
			grantOpen = false;
			await load();
		} catch (e) {
			grantError = e instanceof Error ? e.message : String(e);
		} finally {
			granting = false;
		}
	}

	async function openRevoke(grant: Access) {
		revokeCatalog = grant;
		revokeImpact = null;
		revokeOpen = true;
		if (!ws) return;
		const { data } = await client.GET('/workspaces/{id}/catalog-access/{catalog_id}/impact', {
			params: { path: { id: ws.id, catalog_id: grant.catalog_id } }
		});
		revokeImpact = data ?? null;
	}

	async function confirmRevoke() {
		if (!ws || !revokeCatalog) return;
		const { error: err } = await client.DELETE('/workspaces/{id}/catalog-access/{catalog_id}', {
			params: { path: { id: ws.id, catalog_id: revokeCatalog.catalog_id } }
		});
		if (err) throw new Error('Could not revoke access');
		await load();
	}

	async function confirmDelete() {
		if (!ws) return;
		const { error: err } = await client.DELETE('/workspaces/{id}', {
			params: { path: { id: ws.id } }
		});
		if (err) throw new Error('Could not delete the workspace');
		await workspaceStore.load();
		window.location.href = '/sql';
	}

	onMount(() => {
		const requested = page.url.searchParams.get('tab');
		if (requested && ['general', 'target', 'catalogs', 'members', 'activity'].includes(requested)) {
			tab = requested as Tab;
		}
		void load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	{#if loading && !ws}
		<Skeleton class="h-24 w-full" />
	{:else if error && !ws}
		<ErrorState
			title={m.settings_workspaces_detail_could_not_load_the_workspace()}
			message={error}
			onretry={load}
		/>
	{:else if ws}
		<div class="mb-4">
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{ws.name}</h1>
			<p class="mt-1 text-[12px] text-slate-600 dark:text-slate-300">
				{m.settings_workspaces_detail_created({
					value:
						ws.type === 'personal'
							? m.settings_workspaces_personal_workspace()
							: m.settings_workspaces_shared_workspace()
				})}
				<RelativeTime at={ws.created_at} />
			</p>
		</div>

		<div
			class="mb-4 flex gap-1 border-b border-border"
			role="tablist"
			aria-label={m.settings_workspaces_detail_workspace_tabs()}
		>
			{#each [['general', 'General'], ['target', m.settings_workspaces_default_target()], ['catalogs', 'Catalogs'], ['members', 'Members'], ['activity', 'Activity']] as [value, label] (value)}
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

		{#if tab === 'general'}
			<div class="max-w-xl rounded-lg border border-border bg-bg-panel p-4">
				<div class="flex flex-col gap-3 text-[12px]">
					<Input
						label={m.settings_workspaces_detail_name()}
						bind:value={name}
						disabled={!canEdit}
					/>
					<Input
						label={m.settings_workspaces_detail_description()}
						bind:value={description}
						disabled={!canEdit}
					/>
					<dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
						<dt class="text-slate-600 dark:text-slate-300">
							{m.settings_workspaces_detail_type()}
						</dt>
						<dd>{ws.type}</dd>
						<dt class="text-slate-600 dark:text-slate-300">
							{m.settings_workspaces_detail_owner()}
						</dt>
						<dd class="font-mono">{ws.owner_team_id ?? ws.owner_user_id ?? '—'}</dd>
						<dt class="text-slate-600 dark:text-slate-300">
							{m.settings_workspaces_detail_updated()}
						</dt>
						<dd>{ws.updated_at ? new Date(ws.updated_at).toLocaleString() : '—'}</dd>
					</dl>
					{#if editError}<p class="text-danger" role="alert">{editError}</p>{/if}
					{#if canEdit}
						<div class="flex justify-end">
							<Button variant="primary" loading={saving} disabled={saving} onclick={saveGeneral}>
								{m.settings_workspaces_detail_save()}
							</Button>
						</div>
					{/if}
				</div>
			</div>
			{#if canDelete && ws.type === 'shared'}
				<div class="mt-4 max-w-xl rounded-lg border border-danger/40 bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold text-danger">
						{m.settings_workspaces_detail_danger_zone()}
					</h2>
					<p class="mt-1 text-[11px] text-slate-600 dark:text-slate-300">
						{m.settings_workspaces_detail_deleting_removes_the_workspace_s_spaces()}
					</p>
					<div class="mt-2">
						<Button variant="danger" onclick={() => (deleteOpen = true)}
							>{m.settings_workspaces_detail_delete_workspace()}</Button
						>
					</div>
				</div>
			{/if}
		{:else if tab === 'target'}
			<div class="max-w-xl rounded-lg border border-border bg-bg-panel p-4 text-[12px]">
				<p class="text-[11px] text-slate-600 dark:text-slate-300">
					{m.settings_workspaces_detail_current_default()}
					{#if currentTarget}
						<span class="font-mono">{currentTarget.catalog_name} · {currentTarget.engine_name}</span
						>
					{:else}
						{m.settings_workspaces_detail_none()}
					{/if}
				</p>
				<div class="mt-3 flex flex-col gap-3">
					<Select
						label={m.settings_workspaces_detail_default_target()}
						bind:value={targetBindingId}
						options={[
							{ value: '', label: m.settings_workspaces_no_default_the_only_target_applies() },
							...targets.map((target) => ({
								value: target.binding_id,
								label: `${target.catalog_name} · ${target.engine_name} (${target.permission})`
							}))
						]}
					/>
					<Input
						label={m.settings_workspaces_detail_default_schema()}
						bind:value={targetSchema}
						class="font-mono"
					/>
					{#if targetError}<p class="text-danger" role="alert">{targetError}</p>{/if}
					<div class="flex justify-end gap-2">
						<Button
							onclick={() => {
								targetBindingId = '';
								targetSchema = '';
								void saveTarget();
							}}
							disabled={targetSaving}
						>
							{m.settings_workspaces_detail_clear()}
						</Button>
						<Button
							variant="primary"
							loading={targetSaving}
							disabled={targetSaving}
							onclick={saveTarget}
						>
							{m.settings_workspaces_detail_save()}
						</Button>
					</div>
				</div>
			</div>
		{:else if tab === 'catalogs'}
			<div class="mb-3 flex items-center justify-between">
				<p class="text-[12px] text-slate-600 dark:text-slate-300">
					{m.settings_workspaces_detail_catalogs_granted_to_this_workspace_revoking()}
				</p>
				<Button variant="primary" onclick={openGrant}
					>{m.settings_workspaces_detail_grant_access()}</Button
				>
			</div>
			{#if grants.length === 0}
				<EmptyState
					title={m.settings_workspaces_detail_no_catalog_grants()}
					description={m.settings_workspaces_detail_grant_a_catalog_so_the_workspace()}
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-3 py-2">{m.settings_workspaces_detail_catalog()}</th>
								<th class="px-3 py-2">{m.settings_workspaces_detail_permission()}</th>
								<th class="px-3 py-2">{m.settings_workspaces_detail_granted()}</th>
								<th class="px-3 py-2 text-right">{m.settings_workspaces_detail_actions()}</th>
							</tr>
						</thead>
						<tbody>
							{#each grants as grant (grant.catalog_id)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-3 py-1.5">{grant.catalog_name ?? grant.catalog_id}</td>
									<td class="px-3 py-1.5"><Badge tone="info">{grant.permission}</Badge></td>
									<td class="px-3 py-1.5"><RelativeTime at={grant.granted_at} /></td>
									<td class="px-3 py-1.5 text-right">
										<button
											type="button"
											class="text-danger hover:underline"
											onclick={() => openRevoke(grant)}
										>
											{m.settings_workspaces_detail_revoke()}
										</button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{:else if tab === 'members'}
			{#if ws.type === 'personal'}
				<p class="text-[12px]">{m.settings_workspaces_detail_this_is_a_personal_workspace_its()}</p>
			{:else if members.length === 0}
				<EmptyState
					title={m.settings_workspaces_detail_no_members()}
					description={m.settings_workspaces_detail_team_membership_is_managed_in_settings()}
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr
								><th class="px-3 py-2">{m.settings_workspaces_detail_member()}</th><th
									class="px-3 py-2">{m.settings_workspaces_detail_team_role()}</th
								></tr
							>
						</thead>
						<tbody>
							{#each members as member (member.user.id)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-3 py-1.5"
										>{member.user.name}
										<span class="text-slate-600 dark:text-slate-300">({member.user.email})</span
										></td
									>
									<td class="px-3 py-1.5">{member.role}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{:else if tab === 'activity'}
			<ActivityPanel
				resourceType="workspace"
				resourceId={workspaceId}
				title={m.settings_workspaces_detail_workspace_activity()}
			/>
		{/if}
	{/if}
</div>

<Modal bind:open={grantOpen} title={m.common_grant_catalog_access()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<Select
			label={m.common_catalog()}
			bind:value={grantCatalogId}
			options={grantCatalogs
				.filter((catalog) => !!catalog.id)
				.map((catalog) => ({ value: catalog.id!, label: catalog.name ?? catalog.id! }))}
		/>
		<Select
			label={m.common_permission()}
			bind:value={grantPermission}
			options={[
				{
					value: 'read',
					label: m.settings_workspaces_read_read_only_sessions_where_the_engine_e()
				},
				{ value: 'write', label: m.settings_workspaces_write_queries_may_modify_data() }
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

<ConfirmDialog
	bind:open={revokeOpen}
	title={m.common_revoke_catalog_access()}
	resourceName={revokeCatalog?.catalog_name ?? ''}
	confirmLabel={m.settings_workspaces_detail_revoke()}
	consequences={[
		`${revokeImpact?.saved_queries ?? 0} saved quer(ies) target this catalog and fall back to the workspace default.`,
		`${revokeImpact?.metrics ?? 0} metric(s) use it.`,
		revokeImpact?.default_target_affected
			? 'The workspace default target uses this catalog and will be cleared.'
			: 'The workspace default target is not affected.',
		`${revokeImpact?.running_executions ?? 0} execution(s) are running and continue to completion.`
	]}
	onconfirm={confirmRevoke}
/>

<ConfirmDialog
	bind:open={deleteOpen}
	title={m.settings_workspaces_delete_workspace()}
	resourceName={ws?.name ?? ''}
	confirmLabel={m.settings_workspaces_detail_delete_workspace()}
	typeToConfirm
	consequences={[
		`${impact?.spaces ?? 0} space(s), ${impact?.saved_queries ?? 0} saved quer(ies) and ${impact?.metrics ?? 0} metric(s) are deleted.`,
		`${impact?.running_executions ?? 0} execution(s) must finish first.`,
		'Grants and defaults are removed with the workspace.'
	]}
	onconfirm={confirmDelete}
/>
