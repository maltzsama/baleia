<script lang="ts">
	// Baleia service accounts (#653): non-human principals agents and other
	// clients sign in as. A global admin manages all of them; a workspace admin
	// those created in their workspace. The server decides; this page follows.
	import { onMount } from 'svelte';
	import { m } from '$lib/paraglide/messages';
	import { apiErrorMessage, client } from '$lib/api/client';
	import type { components } from '$lib/api/schema';
	import { canGlobal } from '$lib/authz/can';
	import { authStore } from '$lib/stores/auth.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import OneTimeSecret from '$lib/ui/patterns/OneTimeSecret.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	type ServiceAccount = components['schemas']['ServiceAccount'];
	type Token = components['schemas']['ApiKey'];
	type Provider = components['schemas']['AuthProvider'];
	type Role = components['schemas']['WorkspaceRole'];

	let accounts = $state.raw<ServiceAccount[]>([]);
	let providers = $state.raw<Provider[]>([]);
	let loading = $state(true);
	let loadError = $state('');

	let createOpen = $state(false);
	let name = $state('');
	let description = $state('');
	let workspaceId = $state('');
	let role = $state<Role>('viewer');
	let providerId = $state('');
	let clientId = $state('');
	let saving = $state(false);
	let formError = $state('');

	let selected = $state<ServiceAccount | null>(null);
	let tokens = $state.raw<Token[]>([]);
	let tokenName = $state('');
	let tokenError = $state('');
	let secret = $state('');
	let secretOpen = $state(false);
	let revokeOpen = $state(false);
	let revoking = $state<Token | null>(null);
	let deleteOpen = $state(false);

	// Global admins manage every service account (users are theirs to manage).
	const isAdmin = $derived(canGlobal(authStore.user, 'manage_users'));
	const adminWorkspaces = $derived(
		workspaceStore.workspaces.filter((w) => w.allowed_actions?.includes('manage_members'))
	);
	const oidcProviders = $derived(providers.filter((p) => p.type === 'oidc'));
	const roleOptions = $derived([
		{ value: 'viewer', label: m.workspace_role_viewer() },
		{ value: 'editor', label: m.workspace_role_editor() },
		{ value: 'admin', label: m.workspace_role_admin() }
	]);

	async function load() {
		loading = true;
		loadError = '';
		try {
			const [list, provs] = await Promise.all([
				client.GET('/service-accounts'),
				client.GET('/auth/providers'),
				workspaceStore.load()
			]);
			if (list.error) throw list.error;
			accounts = list.data ?? [];
			providers = provs.data ?? [];
		} catch (e) {
			loadError = apiErrorMessage(e, m.service_accounts_load_failed());
		} finally {
			loading = false;
		}
	}

	function openCreate() {
		name = '';
		description = '';
		workspaceId = isAdmin ? '' : (adminWorkspaces[0]?.id ?? '');
		role = 'viewer';
		providerId = '';
		clientId = '';
		formError = '';
		createOpen = true;
	}

	async function create(event: SubmitEvent) {
		event.preventDefault();
		if (saving) return;
		saving = true;
		formError = '';
		try {
			const { data, error } = await client.POST('/service-accounts', {
				body: {
					name: name.trim(),
					description: description.trim() || undefined,
					workspace_id: workspaceId || undefined,
					role: workspaceId ? role : undefined,
					identity_provider_id: providerId || undefined,
					client_id: providerId ? clientId.trim() : undefined
				}
			});
			if (error) {
				formError = apiErrorMessage(error, m.service_accounts_save_failed());
				return;
			}
			createOpen = false;
			await load();
			if (data) await select(data);
		} catch (e) {
			formError = apiErrorMessage(e, m.service_accounts_save_failed());
		} finally {
			saving = false;
		}
	}

	async function select(account: ServiceAccount) {
		selected = account;
		tokenError = '';
		providerId = account.identity_provider_id ?? '';
		clientId = account.client_id ?? '';
		try {
			const { data, error } = await client.GET('/service-accounts/{id}/tokens', {
				params: { path: { id: account.id } }
			});
			if (error) throw error;
			tokens = data ?? [];
		} catch (e) {
			tokenError = apiErrorMessage(e, m.service_accounts_load_failed());
		}
	}

	async function saveLink(event: SubmitEvent) {
		event.preventDefault();
		if (!selected) return;
		tokenError = '';
		try {
			const { data, error } = await client.PATCH('/service-accounts/{id}', {
				params: { path: { id: selected.id } },
				body: {
					name: selected.name,
					description: selected.description ?? null,
					identity_provider_id: providerId || null,
					client_id: providerId ? clientId.trim() : null
				}
			});
			if (error) {
				tokenError = apiErrorMessage(error, m.service_accounts_save_failed());
				return;
			}
			if (data) selected = data;
			await load();
		} catch (e) {
			tokenError = apiErrorMessage(e, m.service_accounts_save_failed());
		}
	}

	async function issueToken(event: SubmitEvent) {
		event.preventDefault();
		if (!selected || !tokenName.trim()) return;
		tokenError = '';
		try {
			const { data, error } = await client.POST('/service-accounts/{id}/tokens', {
				params: { path: { id: selected.id } },
				body: { name: tokenName.trim() }
			});
			if (error) {
				tokenError = apiErrorMessage(error, m.service_accounts_save_failed());
				return;
			}
			tokenName = '';
			if (data) {
				secret = data.secret;
				secretOpen = true;
			}
			await select(selected);
		} catch (e) {
			tokenError = apiErrorMessage(e, m.service_accounts_save_failed());
		}
	}

	async function revoke() {
		if (!selected || !revoking) return;
		const { error } = await client.DELETE('/service-accounts/{id}/tokens/{token_id}', {
			params: { path: { id: selected.id, token_id: revoking.id } }
		});
		if (error) throw error;
		revoking = null;
		await select(selected);
	}

	async function remove() {
		if (!selected) return;
		const { error } = await client.DELETE('/service-accounts/{id}', {
			params: { path: { id: selected.id } }
		});
		if (error) throw error;
		selected = null;
		await load();
	}

	onMount(() => {
		void load();
	});
</script>

<div class="flex flex-col gap-4 p-4 text-[12px]">
	<div class="flex items-start justify-between gap-3">
		<div>
			<h1 class="text-[15px] font-semibold">{m.settings_sections_service_accounts()}</h1>
			<p class="max-w-2xl text-slate-600 dark:text-slate-300">{m.service_accounts_intro()}</p>
		</div>
		{#if isAdmin || adminWorkspaces.length > 0}
			<Button variant="primary" onclick={openCreate}>{m.service_accounts_new()}</Button>
		{/if}
	</div>

	{#if loading}
		<Skeleton height="6rem" />
	{:else if loadError}
		<ErrorState message={loadError} onretry={load} />
	{:else if accounts.length === 0}
		<EmptyState title={m.service_accounts_none()} description={m.service_accounts_none_hint()} />
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[11px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">{m.service_accounts_name()}</th>
						<th class="px-3 py-2">{m.service_accounts_signs_in_as()}</th>
						<th class="px-3 py-2">{m.service_accounts_created()}</th>
						<th class="px-3 py-2"><span class="sr-only">{m.common_action()}</span></th>
					</tr>
				</thead>
				<tbody>
					{#each accounts as account (account.id)}
						<tr class="border-b border-border/60 last:border-b-0">
							<td class="px-3 py-1.5">
								<div class="font-medium">{account.name}</div>
								<div class="font-mono text-[10px] text-slate-600 dark:text-slate-300">
									{account.email}
								</div>
							</td>
							<td class="px-3 py-1.5">
								{account.client_id
									? m.service_accounts_idp_client({ client: account.client_id })
									: m.service_accounts_baleia_tokens_only()}
							</td>
							<td class="px-3 py-1.5"><RelativeTime at={account.created_at} /></td>
							<td class="px-3 py-1.5 text-right">
								<Button size="sm" variant="ghost" onclick={() => select(account)}>
									{m.service_accounts_manage()}
								</Button>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}

	{#if selected}
		<section class="flex flex-col gap-3 rounded-lg border border-border bg-bg-panel p-3">
			<div class="flex items-center justify-between gap-2">
				<h2 class="text-[13px] font-semibold">{selected.name}</h2>
				<Button size="sm" variant="danger" onclick={() => (deleteOpen = true)}>
					{m.service_accounts_delete()}
				</Button>
			</div>
			<p class="text-slate-600 dark:text-slate-300">
				{m.service_accounts_membership_hint({ email: selected.email })}
			</p>

			<form class="flex flex-wrap items-end gap-2" onsubmit={saveLink}>
				<Select
					label={m.service_accounts_identity_provider()}
					bind:value={providerId}
					options={[
						{ value: '', label: m.service_accounts_no_identity_provider() },
						...oidcProviders.map((p) => ({ value: p.id, label: p.name }))
					]}
				/>
				{#if providerId}
					<Input label={m.service_accounts_client_id()} bind:value={clientId} required />
				{/if}
				<Button type="submit">{m.common_save()}</Button>
			</form>

			<h3 class="text-[12px] font-semibold">{m.service_accounts_tokens()}</h3>
			<form class="flex flex-wrap items-end gap-2" onsubmit={issueToken}>
				<Input label={m.service_accounts_token_name()} bind:value={tokenName} required />
				<Button type="submit" variant="primary" disabled={!tokenName.trim()}>
					{m.service_accounts_issue_token()}
				</Button>
			</form>
			{#if tokenError}<p class="text-danger" role="alert">{tokenError}</p>{/if}
			{#if tokens.length === 0}
				<p class="text-slate-600 dark:text-slate-300">{m.service_accounts_no_tokens()}</p>
			{:else}
				<ul class="flex flex-col gap-1">
					{#each tokens as token (token.id)}
						<li class="flex items-center justify-between gap-2">
							<span>
								{token.name}
								<span class="font-mono text-slate-600 dark:text-slate-300">{token.key_prefix}…</span
								>
							</span>
							<Button
								size="sm"
								variant="ghost"
								onclick={() => {
									revoking = token;
									revokeOpen = true;
								}}>{m.service_accounts_revoke()}</Button
							>
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	{/if}
</div>

<Modal bind:open={createOpen} title={m.service_accounts_new()}>
	<form class="flex flex-col gap-3 text-[12px]" onsubmit={create}>
		<Input label={m.service_accounts_name()} bind:value={name} required />
		<Input label={m.service_accounts_description()} bind:value={description} />
		<Select
			label={m.service_accounts_workspace()}
			bind:value={workspaceId}
			options={[
				...(isAdmin ? [{ value: '', label: m.service_accounts_no_workspace() }] : []),
				...adminWorkspaces.map((w) => ({ value: w.id, label: w.name }))
			]}
		/>
		{#if workspaceId}
			<Select label={m.workspace_members_role()} bind:value={role} options={roleOptions} />
		{/if}
		<Select
			label={m.service_accounts_identity_provider()}
			bind:value={providerId}
			options={[
				{ value: '', label: m.service_accounts_no_identity_provider() },
				...oidcProviders.map((p) => ({ value: p.id, label: p.name }))
			]}
		/>
		{#if providerId}
			<Input label={m.service_accounts_client_id()} bind:value={clientId} required />
		{/if}
		{#if formError}<p class="text-danger" role="alert">{formError}</p>{/if}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (createOpen = false)}>{m.common_cancel()}</Button>
			<Button type="submit" variant="primary" disabled={saving || !name.trim()}>
				{m.service_accounts_create()}
			</Button>
		</div>
	</form>
</Modal>

<OneTimeSecret bind:open={secretOpen} {secret} onacknowledge={() => (secret = '')} />

<ConfirmDialog
	bind:open={revokeOpen}
	title={m.service_accounts_revoke()}
	resourceName={revoking?.name ?? ''}
	consequences={[m.service_accounts_revoke_consequence()]}
	onconfirm={revoke}
/>

<ConfirmDialog
	bind:open={deleteOpen}
	title={m.service_accounts_delete()}
	resourceName={selected?.name ?? ''}
	consequences={[m.service_accounts_delete_consequence()]}
	typeToConfirm
	onconfirm={remove}
/>
