<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import IdentityProviderForm from '$lib/identity/IdentityProviderForm.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';

	type Provider = components['schemas']['IdentityProvider'];
	type Detail = components['schemas']['IdentityProviderDetail'];
	type Create = components['schemas']['CreateIdentityProviderRequest'];
	type Update = components['schemas']['UpdateIdentityProviderRequest'];
	type TestResult = components['schemas']['IdentityProviderTestResult'];

	let providers = $state<Provider[]>([]);
	let loading = $state(true);
	let error = $state('');
	let formOpen = $state(false);
	let editing = $state<Detail | null>(null);
	let deleteOpen = $state(false);
	let deleting = $state<Provider | null>(null);
	let testResults = $state<Record<string, TestResult>>({});
	let testing = $state<string | null>(null);

	async function load() {
		loading = true;
		error = '';
		try {
			const { data, error: err } = await client.GET('/admin/identity-providers');
			if (err || !data) {
				error = apiErrorMessage(err, m.settings_identity_could_not_load_identity_providers());
				return;
			}
			providers = data;
		} catch {
			error = m.settings_identity_could_not_load_identity_providers();
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function openCreate() {
		editing = null;
		formOpen = true;
	}

	async function openEdit(provider: Provider) {
		const { data, error: err } = await client.GET('/admin/identity-providers/{id}', {
			params: { path: { id: provider.id } }
		});
		if (err || !data) {
			error = apiErrorMessage(err, m.settings_identity_could_not_load_the_provider());
			return;
		}
		editing = data;
		formOpen = true;
	}

	async function save(body: Create | Update) {
		if (editing) {
			const { error: err } = await client.PATCH('/admin/identity-providers/{id}', {
				params: { path: { id: editing.id } },
				body: body as Update
			});
			if (err)
				throw new Error(apiErrorMessage(err, m.settings_identity_could_not_save_the_provider()));
		} else {
			const { error: err } = await client.POST('/admin/identity-providers', {
				body: body as Create
			});
			if (err)
				throw new Error(apiErrorMessage(err, m.settings_identity_could_not_create_the_provider()));
		}
		await load();
	}

	async function toggle(provider: Provider) {
		const { error: err } = await client.PATCH('/admin/identity-providers/{id}', {
			params: { path: { id: provider.id } },
			body: { enabled: !provider.enabled }
		});
		if (err) {
			error = apiErrorMessage(err, m.settings_identity_could_not_update_the_provider());
			return;
		}
		await load();
	}

	async function test(provider: Provider) {
		testing = provider.id;
		try {
			const { data, error: err } = await client.POST('/admin/identity-providers/{id}/test', {
				params: { path: { id: provider.id } }
			});
			if (err || !data) {
				testResults = {
					...testResults,
					[provider.id]: {
						ok: false,
						checks: [
							{
								name: 'request',
								status: 'failed',
								message: apiErrorMessage(err, m.settings_identity_probe_failed())
							}
						]
					}
				};
				return;
			}
			testResults = { ...testResults, [provider.id]: data };
		} finally {
			testing = null;
		}
	}

	function askDelete(provider: Provider) {
		deleting = provider;
		deleteOpen = true;
	}

	async function confirmDelete() {
		if (!deleting) return;
		const { error: err } = await client.DELETE('/admin/identity-providers/{id}', {
			params: { path: { id: deleting.id }, query: { suspend_users: true } }
		});
		if (err)
			throw new Error(apiErrorMessage(err, m.settings_identity_could_not_delete_the_provider()));
		deleteOpen = false;
		await load();
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<!-- Lists and tables use the full width (#468). -->
	<div class="w-full">
		<div class="flex items-start justify-between gap-4">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">
					{m.settings_identity_identity_providers()}
				</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					{m.settings_identity_oidc_and_ldap_providers_that_can_authentic()}
				</p>
			</div>
			<Button variant="primary" onclick={openCreate}>{m.settings_identity_add_provider()}</Button>
		</div>

		{#if loading}
			<div class="mt-6 flex flex-col gap-3">
				<Skeleton height="4rem" />
				<Skeleton height="4rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState
					title={m.settings_identity_could_not_load_identity_providers_2()}
					message={error}
					onretry={load}
				/>
			</div>
		{:else if providers.length === 0}
			<div class="mt-6">
				<EmptyState
					title={m.settings_identity_no_identity_providers()}
					description={m.settings_identity_users_sign_in_locally_until_an()}
				/>
			</div>
		{:else}
			<div class="mt-6 flex flex-col gap-3">
				{#each providers as provider (provider.id)}
					<article class="rounded-lg border border-border bg-bg-panel p-4">
						<div class="flex flex-wrap items-start justify-between gap-3">
							<div>
								<div class="flex items-center gap-2">
									<h2 class="text-[14px] font-semibold">{provider.name}</h2>
									<Badge tone={provider.type === 'oidc' ? 'info' : 'neutral'}>
										{provider.type.toUpperCase()}
									</Badge>
									<Badge tone={provider.enabled ? 'success' : 'neutral'}>
										{provider.enabled
											? m.settings_identity_enabled()
											: m.settings_identity_disabled()}
									</Badge>
								</div>
								<p class="mt-1 text-[12px] text-text-muted">
									{provider.linked_users}
									{provider.linked_users === 1
										? m.settings_identity_linked_user()
										: m.settings_identity_linked_users()}
									{#if provider.has_client_secret || provider.has_bind_password}
										{m.settings_identity_secret_stored()}
									{/if}
								</p>
							</div>
							<div class="flex flex-wrap gap-2">
								<Button size="sm" loading={testing === provider.id} onclick={() => test(provider)}
									>{m.settings_identity_test()}</Button
								>
								<Button size="sm" onclick={() => openEdit(provider)}
									>{m.settings_identity_edit()}</Button
								>
								<Button size="sm" onclick={() => toggle(provider)}>
									{provider.enabled ? m.settings_identity_disable() : m.settings_identity_enable()}
								</Button>
								<Button size="sm" variant="danger" onclick={() => askDelete(provider)}
									>{m.settings_identity_delete()}</Button
								>
							</div>
						</div>

						{#if testResults[provider.id]}
							<ul class="mt-3 flex flex-col gap-1 border-t border-border pt-3">
								{#each testResults[provider.id].checks as check (check.name)}
									<li class="flex items-center gap-2 text-[12px]">
										<Badge
											tone={check.status === 'ok'
												? 'success'
												: check.status === 'failed'
													? 'danger'
													: 'neutral'}
										>
											{check.status}
										</Badge>
										<span class="font-medium">{check.name}</span>
										{#if check.message}
											<span class="text-text-muted">{check.message}</span>
										{/if}
									</li>
								{/each}
							</ul>
						{/if}
					</article>
				{/each}
			</div>
		{/if}
	</div>
</div>

<IdentityProviderForm bind:open={formOpen} provider={editing} onsubmit={save} />

<ConfirmDialog
	bind:open={deleteOpen}
	title={m.settings_identity_delete_identity_provider()}
	resourceName={deleting?.name ?? ''}
	consequences={[
		m.settings_identity_users_linked_to_this_provider_are(),
		m.settings_identity_their_refresh_tokens_and_api_keys_are_revo(),
		m.settings_identity_the_provider_configuration_and_stored_secr()
	]}
	confirmLabel={m.settings_identity_delete_provider()}
	onconfirm={confirmDelete}
/>
