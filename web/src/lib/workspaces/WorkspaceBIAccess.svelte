<script lang="ts">
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { m } from '$lib/paraglide/messages';
	import { authStore } from '$lib/stores/auth.svelte';
	import Badge from '$lib/ui/Badge.svelte';
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
	import { toasts } from '$lib/ui/toast.svelte';
	import { canOpenInBI } from '$lib/ui/patterns/kindIcon';
	import SetupGuide from '$lib/bi/SetupGuide.svelte';

	type BiStatus = components['schemas']['WorkspaceBIStatus'];
	type BiKey = components['schemas']['BiKey'];
	type Target = components['schemas']['WorkspaceTarget'];
	type Blocker = components['schemas']['WorkspaceBIGatewayBlocker'];

	let {
		workspaceId,
		canEdit,
		onchange
	}: {
		workspaceId: string;
		/** The workspace update action; only then the BI switch is offered. */
		canEdit: boolean;
		onchange?: () => void;
	} = $props();

	let status = $state<BiStatus | null>(null);
	let keys = $state<BiKey[]>([]);
	let targets = $state<Target[]>([]);
	let loading = $state(true);
	let error = $state('');

	let createOpen = $state(false);
	let name = $state('');
	let engineId = $state('');
	let expires = $state('');
	let creating = $state(false);
	let createError = $state('');

	let secret = $state<string | null>(null);
	let secretOpen = $state(false);

	let revokeTarget = $state<BiKey | null>(null);
	let revokeOpen = $state(false);

	let switchOpen = $state(false);

	const currentUserId = $derived(authStore.user?.id ?? '');
	const workspaceHref = $derived(`/settings/workspaces/${workspaceId}?tab=bi`);
	const enabled = $derived(status?.workspace_bi_enabled ?? false);
	const engines = $derived.by(() => {
		const list: { value: string; label: string }[] = [];
		for (const target of targets) {
			if (!list.some((engine) => engine.value === target.engine_id)) {
				list.push({ value: target.engine_id, label: target.engine_name });
			}
		}
		return list;
	});

	async function load() {
		loading = true;
		error = '';
		try {
			const [statusRes, keysRes, targetsRes] = await Promise.all([
				client.GET('/workspaces/{id}/bi-status', { params: { path: { id: workspaceId } } }),
				client.GET('/workspaces/{id}/bi-keys', { params: { path: { id: workspaceId } } }),
				client.GET('/workspaces/{id}/targets', { params: { path: { id: workspaceId } } })
			]);
			if (statusRes.error || !statusRes.data) {
				error = apiErrorMessage(statusRes.error, m.workspace_bi_load_failed());
				return;
			}
			status = statusRes.data;
			keys = keysRes.data ?? [];
			targets = (targetsRes.data ?? []).filter((target) => canOpenInBI(target.engine_kind));
		} catch {
			error = m.workspace_bi_load_failed();
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function engineName(engine: string): string {
		return (
			targets.find((target) => target.engine_id === engine)?.engine_name ??
			m.workspace_bi_engine_missing()
		);
	}

	function isExpired(key: BiKey): boolean {
		return !!key.expires_at && new Date(key.expires_at).getTime() < Date.now();
	}

	function canRevoke(key: BiKey): boolean {
		return !!status?.can_manage_keys || key.owner_id === currentUserId;
	}

	// Blocker codes are a closed contract: each one names the step that fixes it.
	function blockerLabel(blocker: Blocker): string {
		switch (blocker) {
			case 'listener_not_configured':
				return m.workspace_bi_blocker_listener_not_configured();
			case 'secret_not_configured':
				return m.workspace_bi_blocker_secret_not_configured();
			case 'public_url_not_configured':
				return m.workspace_bi_blocker_public_url_not_configured();
			case 'public_url_invalid':
				return m.workspace_bi_blocker_public_url_invalid();
			case 'gateway_disabled':
				return m.workspace_bi_blocker_gateway_disabled();
			case 'no_trino_targets':
				return m.workspace_bi_blocker_no_trino_targets();
			default:
				return m.workspace_bi_blocker_workspace_bi_disabled();
		}
	}

	function openCreate() {
		engineId = engines[0]?.value ?? '';
		createError = '';
		createOpen = true;
	}

	async function createKey() {
		creating = true;
		createError = '';
		try {
			const expiresAt = expires ? new Date(`${expires}T23:59:59Z`).toISOString() : undefined;
			const { data, error: err } = await client.POST('/workspaces/{id}/bi-keys', {
				params: { path: { id: workspaceId } },
				body: { name: name.trim(), engine_id: engineId, expires_at: expiresAt ?? null }
			});
			if (err || !data) {
				createError = apiErrorMessage(err, m.workspace_bi_create_failed());
				return;
			}
			createOpen = false;
			name = '';
			engineId = '';
			expires = '';
			// The secret lives only in this modal until it is acknowledged (#287).
			secret = data.secret;
			secretOpen = true;
			await load();
		} finally {
			creating = false;
		}
	}

	function askRevoke(key: BiKey) {
		revokeTarget = key;
		revokeOpen = true;
	}

	async function confirmRevoke() {
		if (!revokeTarget) return;
		const { error: err } = await client.DELETE('/workspaces/{id}/bi-keys/{key_id}', {
			params: { path: { id: workspaceId, key_id: revokeTarget.id } }
		});
		if (err) throw new Error(apiErrorMessage(err, m.workspace_bi_revoke_failed()));
		toasts.success(m.workspace_bi_revoked());
		await load();
	}

	async function setWorkspaceBI(next: boolean) {
		const { error: err } = await client.PATCH('/workspaces/{id}', {
			params: { path: { id: workspaceId } },
			body: { bi_enabled: next }
		});
		if (err) throw new Error(apiErrorMessage(err, m.workspace_bi_update_failed()));
		await load();
		onchange?.();
	}
</script>

<div class="flex flex-col gap-4">
	<div class="flex flex-wrap items-end justify-between gap-3">
		<div>
			<h2 class="text-[15px] font-semibold">{m.workspace_bi_title()}</h2>
			<p class="mt-1 text-[12px] text-slate-600 dark:text-slate-300">
				{m.workspace_bi_description()}
			</p>
		</div>
		{#if status}
			<Badge tone={status.state === 'ready' ? 'success' : 'danger'}>
				{status.state === 'ready'
					? m.settings_bi_gateway_state_ready()
					: m.settings_bi_gateway_state_blocked()}
			</Badge>
		{/if}
	</div>

	{#if loading && !status}
		<div class="flex flex-col gap-2">
			<Skeleton height="3rem" />
			<Skeleton height="6rem" />
		</div>
	{:else if error}
		<ErrorState title={m.workspace_bi_load_failed()} message={error} onretry={load} />
	{:else if status}
		{#if status.blocked_by.length > 0}
			<ul
				class="list-disc rounded-lg border border-amber-300 bg-bg-panel py-3 pl-8 pr-4 text-[12px] leading-[1.55] dark:border-amber-700"
			>
				{#each status.blocked_by as blocker (blocker)}
					<li>{blockerLabel(blocker)}</li>
				{/each}
			</ul>
		{/if}

		<div class="rounded-lg border border-border bg-bg-panel p-4">
			<div class="flex flex-wrap items-center justify-between gap-3">
				<div>
					<h3 class="text-[13px] font-semibold">{m.workspace_bi_switch()}</h3>
					<p class="mt-1 text-[11px] text-slate-600 dark:text-slate-300">
						{enabled ? m.workspace_bi_enabled() : m.workspace_bi_disabled()}
					</p>
				</div>
				{#if canEdit}
					<Button variant={enabled ? 'danger' : 'primary'} onclick={() => (switchOpen = true)}>
						{enabled ? m.workspace_bi_disable() : m.workspace_bi_enable()}
					</Button>
				{:else}
					<p class="text-[11px] text-slate-600 dark:text-slate-300">
						{m.workspace_bi_read_only()}
					</p>
				{/if}
			</div>
			<p class="mt-3 text-[11px] text-slate-600 dark:text-slate-300">
				{m.workspace_bi_gateway_explain()}
			</p>
		</div>

		<div>
			<div class="mb-2 flex flex-wrap items-center justify-between gap-2">
				<h3 class="text-[13px] font-semibold">{m.workspace_bi_keys()}</h3>
				{#if status.can_create_keys}
					<Button variant="primary" size="sm" onclick={openCreate}>
						{m.workspace_bi_create_key()}
					</Button>
				{/if}
			</div>
			<p class="mb-2 text-[11px] text-slate-600 dark:text-slate-300">
				{status.can_manage_keys
					? m.workspace_bi_manage_all_hint()
					: m.workspace_bi_manage_own_hint()}
			</p>

			{#if keys.length === 0}
				<EmptyState
					title={m.workspace_bi_no_keys()}
					description={m.workspace_bi_no_keys_description()}
				/>
			{:else}
				<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-text-muted"
						>
							<tr>
								<th class="px-3 py-2">{m.workspace_bi_name()}</th>
								<th class="px-3 py-2">{m.workspace_bi_owner()}</th>
								<th class="px-3 py-2">{m.workspace_bi_engine()}</th>
								<th class="px-3 py-2">{m.workspace_bi_created()}</th>
								<th class="px-3 py-2">{m.workspace_bi_expires()}</th>
								<th class="px-3 py-2">{m.workspace_bi_last_used()}</th>
								<th class="px-3 py-2"></th>
							</tr>
						</thead>
						<tbody>
							{#each keys as key (key.id)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-3 py-2">
										<div class="font-medium">{key.name}</div>
										<code class="text-[10px] text-text-muted">{key.key_prefix}</code>
									</td>
									<td class="px-3 py-2">{key.owner_email}</td>
									<td class="px-3 py-2">{engineName(key.engine_id)}</td>
									<td class="px-3 py-2"><RelativeTime at={key.created_at} /></td>
									<td class="px-3 py-2">
										{#if key.expires_at}
											<span class="inline-flex items-center gap-1.5">
												<RelativeTime at={key.expires_at} />
												{#if isExpired(key)}
													<Badge tone="danger">{m.workspace_bi_expired()}</Badge>
												{/if}
											</span>
										{:else}
											{m.settings_bi_gateway_none()}
										{/if}
									</td>
									<td class="px-3 py-2">
										{#if key.last_used_at}
											<RelativeTime at={key.last_used_at} />
										{:else}
											{m.workspace_bi_never()}
										{/if}
									</td>
									<td class="px-3 py-2 text-right">
										{#if canRevoke(key)}
											<Button size="sm" variant="danger" onclick={() => askRevoke(key)}>
												{m.workspace_bi_revoke()}
											</Button>
										{/if}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}

			<SetupGuide
				gateway={status.gateway}
				workspace={status}
				hasKeys={keys.some((key) => !isExpired(key))}
				{workspaceHref}
				canManageWorkspace={canEdit}
			/>
		</div>
	{/if}
</div>

<Modal bind:open={createOpen} title={m.workspace_bi_create_title()}>
	<div class="flex flex-col gap-3">
		{#if createError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{createError}
			</p>
		{/if}
		<Input label={m.common_name()} bind:value={name} />
		<Select label={m.workspace_bi_engine()} bind:value={engineId} options={engines} />
		<Input label={m.workspace_bi_expires_optional()} type="date" bind:value={expires} />
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (createOpen = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={creating} onclick={createKey}>
				{m.workspace_bi_create_key()}
			</Button>
		</div>
	{/snippet}
</Modal>

<OneTimeSecret
	bind:open={secretOpen}
	secret={secret ?? ''}
	title={m.workspace_bi_copy_secret_title()}
	onacknowledge={() => (secret = null)}
>
	{#snippet details()}
		<p class="text-[12px] text-text-muted">{m.workspace_bi_secret_details()}</p>
	{/snippet}
</OneTimeSecret>

<ConfirmDialog
	bind:open={revokeOpen}
	title={m.workspace_bi_revoke_title()}
	resourceName={revokeTarget?.name ?? ''}
	confirmLabel={m.workspace_bi_revoke()}
	consequences={[m.workspace_bi_revoke_consequence_1(), m.workspace_bi_revoke_consequence_2()]}
	onconfirm={confirmRevoke}
/>

<ConfirmDialog
	bind:open={switchOpen}
	title={enabled ? m.workspace_bi_disable_title() : m.workspace_bi_enable_title()}
	resourceName={m.workspace_bi_title()}
	confirmLabel={enabled ? m.workspace_bi_disable() : m.workspace_bi_enable()}
	danger={enabled}
	consequences={enabled
		? [m.workspace_bi_disable_consequence_1(), m.workspace_bi_disable_consequence_2()]
		: [m.workspace_bi_enable_consequence()]}
	onconfirm={() => setWorkspaceBI(!enabled)}
/>
