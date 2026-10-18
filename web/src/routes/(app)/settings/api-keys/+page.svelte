<script lang="ts">
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import OneTimeSecret from '$lib/ui/patterns/OneTimeSecret.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	type ApiKey = components['schemas']['ApiKey'];

	let keys = $state<ApiKey[]>([]);
	let loading = $state(true);
	let error = $state('');

	let createOpen = $state(false);
	let name = $state('');
	let expires = $state('');
	let creating = $state(false);
	let createError = $state('');

	let secret = $state<string | null>(null);
	let secretOpen = $state(false);

	let revokeTarget = $state<ApiKey | null>(null);
	let revokeOpen = $state(false);

	async function load() {
		loading = true;
		error = '';
		try {
			const { data, error: err } = await client.GET('/api-keys');
			if (err || !data) {
				error = apiErrorMessage(err, 'Could not load API keys.');
				return;
			}
			keys = data;
		} catch {
			error = 'Could not load API keys.';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function createKey() {
		creating = true;
		createError = '';
		try {
			const expiresAt = expires ? new Date(`${expires}T23:59:59Z`).toISOString() : undefined;
			const { data, error: err } = await client.POST('/api-keys', {
				body: { name, expires_at: expiresAt ?? null }
			});
			if (err || !data) {
				createError = apiErrorMessage(err, 'Could not create the key.');
				return;
			}
			createOpen = false;
			name = '';
			expires = '';
			// The secret lives only in this modal until it is acknowledged (#287).
			secret = data.secret;
			secretOpen = true;
			await load();
		} finally {
			creating = false;
		}
	}

	function askRevoke(key: ApiKey) {
		revokeTarget = key;
		revokeOpen = true;
	}

	async function confirmRevoke() {
		if (!revokeTarget) return;
		const { error: err } = await client.DELETE('/api-keys/{id}', {
			params: { path: { id: revokeTarget.id } }
		});
		if (err) throw new Error(apiErrorMessage(err, 'Could not revoke the key.'));
		toasts.success('API key revoked.');
		await load();
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-3xl">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">API keys</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					Keys authenticate scripts and agents as you. The secret is shown once and never stored by
					the UI.
				</p>
			</div>
			<Button variant="primary" onclick={() => (createOpen = true)}>Create key</Button>
		</div>

		{#if loading}
			<div class="mt-6 flex flex-col gap-2">
				<Skeleton height="3rem" />
				<Skeleton height="3rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState title="Could not load API keys" message={error} onretry={load} />
			</div>
		{:else if keys.length === 0}
			<div class="mt-6">
				<EmptyState
					title="No API keys"
					description="Create a key to call the API from a script or an agent."
				/>
			</div>
		{:else}
			<div class="mt-6 overflow-hidden rounded-lg border border-border bg-bg-panel">
				{#each keys as key (key.id)}
					<div
						class="flex flex-wrap items-center justify-between gap-3 border-b border-border p-3 last:border-0"
					>
						<div>
							<div class="font-medium">{key.name}</div>
							<div class="text-[11px] text-text-muted">
								<code>{key.key_prefix}…</code>
								· created <RelativeTime at={key.created_at} />
								{#if key.expires_at}
									· expires <RelativeTime at={key.expires_at} />
								{/if}
							</div>
						</div>
						<Button size="sm" variant="danger" onclick={() => askRevoke(key)}>Revoke</Button>
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>

<Modal bind:open={createOpen} title="Create API key">
	<div class="flex flex-col gap-3">
		{#if createError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{createError}
			</p>
		{/if}
		<Input label="Name" bind:value={name} placeholder="ci-pipeline" />
		<Input label="Expires (optional)" type="date" bind:value={expires} />
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (createOpen = false)}>Cancel</Button>
			<Button variant="primary" loading={creating} onclick={createKey}>Create key</Button>
		</div>
	{/snippet}
</Modal>

<OneTimeSecret
	bind:open={secretOpen}
	secret={secret ?? ''}
	title="Copy this API key now"
	onacknowledge={() => (secret = null)}
/>

<ConfirmDialog
	bind:open={revokeOpen}
	title="Revoke API key"
	resourceName={revokeTarget?.name ?? ''}
	consequences={[
		'Any script or agent using this key stops working immediately.',
		'The key cannot be recovered; create a new one if needed.'
	]}
	confirmLabel="Revoke key"
	onconfirm={confirmRevoke}
/>
