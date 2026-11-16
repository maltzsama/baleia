<script lang="ts">
	import { resolve } from '$app/paths';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { m } from '$lib/paraglide/messages';
	import Button from '$lib/ui/Button.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import OneTimeSecret from '$lib/ui/patterns/OneTimeSecret.svelte';

	type BiStatus = components['schemas']['WorkspaceBIStatus'];
	type BiKey = components['schemas']['BiKey'];
	type BiKeyCreated = components['schemas']['BiKeyCreated'];
	type Blocker = components['schemas']['WorkspaceBIGatewayBlocker'];

	let {
		open = $bindable(false),
		tool,
		workspaceId,
		workspaceName,
		engineId,
		engineName,
		catalog,
		schema,
		table
	}: {
		open?: boolean;
		tool: 'tableau' | 'powerbi';
		workspaceId: string;
		workspaceName: string;
		engineId: string;
		engineName: string;
		catalog: string;
		schema: string;
		table: string;
	} = $props();

	let status = $state<BiStatus | null>(null);
	let keys = $state<BiKey[]>([]);
	let loading = $state(true);
	let error = $state('');

	let selectedKeyId = $state('');
	let newKeyName = $state('');
	let creating = $state(false);
	let createError = $state('');
	let secret = $state<string | null>(null);
	let secretEmail = $state('');
	let secretOpen = $state(false);

	let downloading = $state(false);
	let downloadError = $state('');
	let retryTick = $state(0);

	const title = $derived(
		tool === 'tableau' ? m.bi_connect_title_tableau() : m.bi_connect_title_powerbi()
	);
	const nextSteps = $derived(
		tool === 'tableau' ? m.bi_connect_next_tableau() : m.bi_connect_next_powerbi()
	);
	const secretDetails = $derived(
		tool === 'tableau'
			? m.bi_connect_key_secret_details_tableau({ email: secretEmail })
			: m.bi_connect_key_secret_details_powerbi({ email: secretEmail })
	);
	const ready = $derived(status?.state === 'ready');
	const usableKeys = $derived(keys.filter((key) => key.engine_id === engineId && !isExpired(key)));
	const selectedKey = $derived(usableKeys.find((key) => key.id === selectedKeyId) ?? null);
	const keyOptions = $derived(
		usableKeys.map((key) => ({ value: key.id, label: `${key.name} — ${key.owner_email}` }))
	);
	const gatewayAddress = $derived.by(() => {
		const raw = status?.gateway.public_url;
		if (!raw) return null;
		try {
			const url = new URL(raw);
			return {
				host: url.hostname,
				port: url.port || (url.protocol === 'https:' ? '443' : '80')
			};
		} catch {
			return null;
		}
	});

	function isExpired(key: BiKey): boolean {
		return !!key.expires_at && new Date(key.expires_at).getTime() < Date.now();
	}

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

	$effect(() => {
		if (!open) return;
		void retryTick;
		const id = workspaceId;
		let cancelled = false;
		loading = true;
		error = '';
		downloadError = '';
		void (async () => {
			const [statusRes, keysRes] = await Promise.all([
				client.GET('/workspaces/{id}/bi-status', { params: { path: { id } } }),
				client.GET('/workspaces/{id}/bi-keys', { params: { path: { id } } })
			]);
			if (cancelled) return;
			if (statusRes.error || !statusRes.data) {
				error = apiErrorMessage(statusRes.error, m.bi_connect_load_failed());
				loading = false;
				return;
			}
			status = statusRes.data;
			keys = keysRes.data ?? [];
			const first = keys.find((key) => key.engine_id === engineId && !isExpired(key));
			selectedKeyId = first?.id ?? '';
			loading = false;
		})();
		return () => {
			cancelled = true;
		};
	});

	async function createKey() {
		creating = true;
		createError = '';
		try {
			const { data, error: err } = await client.POST('/workspaces/{id}/bi-keys', {
				params: { path: { id: workspaceId } },
				body: { name: newKeyName.trim(), engine_id: engineId }
			});
			if (err || !data) {
				createError = apiErrorMessage(err, m.bi_connect_create_failed());
				return;
			}
			const created = data as BiKeyCreated;
			keys = [created, ...keys];
			selectedKeyId = created.id;
			newKeyName = '';
			// The secret lives only in this modal until it is acknowledged (#287).
			secret = created.secret;
			secretEmail = created.owner_email;
			secretOpen = true;
		} finally {
			creating = false;
		}
	}

	async function download() {
		downloading = true;
		downloadError = '';
		try {
			const {
				data,
				error: err,
				response
			} = await client.GET('/workspaces/{id}/bi/{tool}', {
				// The file is text/plain even when its body is Power BI's JSON.
				parseAs: 'text',
				params: {
					path: { id: workspaceId, tool },
					query: {
						engine_id: engineId,
						catalog,
						table,
						...(schema ? { schema } : {})
					}
				}
			});
			if (!response.ok) {
				throw new Error(apiErrorMessage(err, m.bi_connect_download_failed()));
			}
			const blob = new Blob([data ?? ''], { type: 'text/plain' });
			const href = URL.createObjectURL(blob);
			const link = document.createElement('a');
			link.href = href;
			link.download = `${table}.${tool === 'tableau' ? 'tds' : 'pbids'}`;
			document.body.appendChild(link);
			link.click();
			link.remove();
			URL.revokeObjectURL(href);
		} catch (e) {
			downloadError = apiErrorMessage(e, m.bi_connect_download_failed());
		} finally {
			downloading = false;
		}
	}
</script>

<Modal bind:open {title} size="lg">
	<div class="flex flex-col gap-4 text-[13px]">
		<p class="text-text-muted">{m.bi_connect_description()}</p>

		{#if loading && !status}
			<div class="flex flex-col gap-2">
				<Skeleton height="3rem" />
				<Skeleton height="6rem" />
			</div>
		{:else if error}
			<ErrorState title={m.bi_connect_load_failed()} message={error} onretry={() => retryTick++} />
		{:else if status}
			<dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[12px]">
				<dt class="text-text-muted">{m.bi_connect_workspace()}</dt>
				<dd>{workspaceName}</dd>
				<dt class="text-text-muted">{m.bi_connect_engine()}</dt>
				<dd>{engineName}</dd>
				<dt class="text-text-muted">{m.bi_connect_catalog()}</dt>
				<dd class="font-mono">{catalog}</dd>
				{#if schema}
					<dt class="text-text-muted">{m.bi_connect_schema()}</dt>
					<dd class="font-mono">{schema}</dd>
				{/if}
				<dt class="text-text-muted">{m.bi_connect_table()}</dt>
				<dd class="font-mono">{table}</dd>
			</dl>

			{#if ready}
				<div class="flex flex-col gap-3 rounded-lg border border-border bg-bg-panel p-3">
					<h3 class="text-[12px] font-semibold">{m.bi_connect_key()}</h3>
					<p class="text-[11px] text-text-muted">{m.bi_connect_key_hint()}</p>
					{#if usableKeys.length > 0}
						<Select
							label={m.bi_connect_select_key()}
							bind:value={selectedKeyId}
							options={keyOptions}
						/>
					{:else}
						<p class="text-[11px] text-text-muted">{m.bi_connect_no_keys()}</p>
					{/if}
					{#if status.can_create_keys}
						<div class="flex flex-wrap items-end gap-2">
							<div class="min-w-[12rem] flex-1">
								<Input label={m.bi_connect_key_name()} bind:value={newKeyName} />
							</div>
							<Button variant="secondary" loading={creating} onclick={createKey}>
								{m.bi_connect_create_key()}
							</Button>
						</div>
					{/if}
					{#if createError}
						<p class="text-[11px] text-danger" role="alert">{createError}</p>
					{/if}
					{#if !selectedKey}
						<p class="text-[11px] text-text-muted">{m.bi_connect_key_required()}</p>
					{/if}
					<div class="flex flex-wrap items-center gap-3">
						<Button
							variant="primary"
							loading={downloading}
							disabled={!selectedKey || downloading}
							onclick={download}
						>
							{m.bi_connect_download()}
						</Button>
						<span class="text-[11px] text-text-muted">{m.bi_connect_read_only()}</span>
					</div>
					{#if downloadError}
						<p class="text-[11px] text-danger" role="alert">{downloadError}</p>
					{/if}
				</div>

				<div class="rounded-lg border border-border bg-bg-panel p-3">
					<h3 class="text-[12px] font-semibold">{m.bi_connect_next_steps()}</h3>
					<p class="mt-1 text-[11px] text-text-muted">{nextSteps}</p>
				</div>

				{#if gatewayAddress}
					<div class="rounded-lg border border-border bg-bg-panel p-3">
						<h3 class="text-[12px] font-semibold">{m.bi_connect_manual()}</h3>
						<p class="mt-1 text-[11px] text-text-muted">{m.bi_connect_manual_hint()}</p>
						<dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
							<dt class="text-text-muted">{m.bi_connect_host()}</dt>
							<dd class="font-mono">{gatewayAddress.host}</dd>
							<dt class="text-text-muted">{m.bi_connect_port()}</dt>
							<dd class="font-mono">{gatewayAddress.port}</dd>
							<dt class="text-text-muted">{m.bi_connect_catalog()}</dt>
							<dd class="font-mono">{catalog}</dd>
							{#if schema}
								<dt class="text-text-muted">{m.bi_connect_schema()}</dt>
								<dd class="font-mono">{schema}</dd>
							{/if}
							<dt class="text-text-muted">{m.bi_connect_auth()}</dt>
							<dd>{m.bi_connect_auth_value()}</dd>
							<dt class="text-text-muted">{m.bi_connect_username()}</dt>
							<dd>{m.bi_connect_username_value()}</dd>
						</dl>
					</div>
				{/if}
			{:else}
				<div class="rounded-lg border border-amber-300 bg-bg-panel p-3 dark:border-amber-700">
					<h3 class="text-[12px] font-semibold">{m.bi_connect_blocked()}</h3>
					<ul class="mt-2 list-disc pl-5 text-[11px] leading-[1.55] text-text-muted">
						{#each status.blocked_by as blocker (blocker)}
							<li>{blockerLabel(blocker)}</li>
						{/each}
					</ul>
					<div class="mt-3 flex flex-wrap gap-3 text-[11px]">
						<a
							class="font-medium text-primary hover:underline"
							href={resolve(`/settings/workspaces/${workspaceId}?tab=bi`)}
						>
							{m.bi_connect_open_workspace()}
						</a>
						<a
							class="font-medium text-primary hover:underline"
							href={resolve('/settings/bi-gateway')}
						>
							{m.bi_connect_open_gateway()}
						</a>
						<a
							class="font-medium text-primary hover:underline"
							href="https://github.com/maltzsama/baleia/blob/main/docs/install.md#bi-gateway"
							target="_blank"
							rel="noreferrer"
						>
							{m.bi_connect_deployment_guide()}
						</a>
					</div>
				</div>
			{/if}
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end">
			<Button onclick={() => (open = false)}>{m.common_close()}</Button>
		</div>
	{/snippet}
</Modal>

<OneTimeSecret
	bind:open={secretOpen}
	secret={secret ?? ''}
	title={m.bi_connect_key_secret_title()}
	onacknowledge={() => (secret = null)}
>
	{#snippet details()}
		<p class="text-[12px] text-text-muted">{secretDetails}</p>
	{/snippet}
</OneTimeSecret>
