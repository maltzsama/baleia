<script lang="ts">
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
				error = apiErrorMessage(err, 'Could not load identity providers.');
				return;
			}
			providers = data;
		} catch {
			error = 'Could not load identity providers.';
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
			error = apiErrorMessage(err, 'Could not load the provider.');
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
			if (err) throw new Error(apiErrorMessage(err, 'Could not save the provider.'));
		} else {
			const { error: err } = await client.POST('/admin/identity-providers', {
				body: body as Create
			});
			if (err) throw new Error(apiErrorMessage(err, 'Could not create the provider.'));
		}
		await load();
	}

	async function toggle(provider: Provider) {
		const { error: err } = await client.PATCH('/admin/identity-providers/{id}', {
			params: { path: { id: provider.id } },
			body: { enabled: !provider.enabled }
		});
		if (err) {
			error = apiErrorMessage(err, 'Could not update the provider.');
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
								message: apiErrorMessage(err, 'probe failed')
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
		if (err) throw new Error(apiErrorMessage(err, 'Could not delete the provider.'));
		deleteOpen = false;
		await load();
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-4xl">
		<div class="flex items-start justify-between gap-4">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">Identity providers</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					OIDC and LDAP providers that can authenticate users. Secrets are written once and never
					shown again.
				</p>
			</div>
			<Button variant="primary" onclick={openCreate}>Add provider</Button>
		</div>

		{#if loading}
			<div class="mt-6 flex flex-col gap-3">
				<Skeleton height="4rem" />
				<Skeleton height="4rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState title="Could not load identity providers" message={error} onretry={load} />
			</div>
		{:else if providers.length === 0}
			<div class="mt-6">
				<EmptyState
					title="No identity providers"
					description="Users sign in locally until an OIDC or LDAP provider is added."
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
										{provider.enabled ? 'Enabled' : 'Disabled'}
									</Badge>
								</div>
								<p class="mt-1 text-[12px] text-text-muted">
									{provider.linked_users}
									{provider.linked_users === 1 ? 'linked user' : 'linked users'}
									{#if provider.has_client_secret || provider.has_bind_password}
										· secret stored
									{/if}
								</p>
							</div>
							<div class="flex flex-wrap gap-2">
								<Button size="sm" loading={testing === provider.id} onclick={() => test(provider)}
									>Test</Button
								>
								<Button size="sm" onclick={() => openEdit(provider)}>Edit</Button>
								<Button size="sm" onclick={() => toggle(provider)}>
									{provider.enabled ? 'Disable' : 'Enable'}
								</Button>
								<Button size="sm" variant="danger" onclick={() => askDelete(provider)}
									>Delete</Button
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
	title="Delete identity provider"
	resourceName={deleting?.name ?? ''}
	consequences={[
		'Users linked to this provider are suspended and detached; they are never deleted.',
		'Their refresh tokens and API keys are revoked immediately.',
		'The provider configuration and stored secret are removed.'
	]}
	confirmLabel="Delete provider"
	onconfirm={confirmDelete}
/>
