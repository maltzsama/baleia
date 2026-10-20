<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	type Agent = components['schemas']['Agent'];
	type AgentToken = components['schemas']['AgentToken'];
	type AgentPolicy = components['schemas']['AgentPolicy'];
	type AgentPolicyResponse = components['schemas']['AgentPolicyResponse'];
	type AgentCatalogEligibility = components['schemas']['AgentCatalogEligibility'];
	type AgentsMeta = components['schemas']['AgentsMeta'];
	type AuditEvent = components['schemas']['AuditEvent'];
	type Catalog = components['schemas']['Catalog'];
	type Engine = components['schemas']['Engine'];

	type Tab = 'overview' | 'tokens' | 'policy' | 'activity';
	const tabs: { id: Tab; label: string }[] = [
		{ id: 'overview', label: 'Overview' },
		{ id: 'tokens', label: 'Tokens' },
		{ id: 'policy', label: 'Policy' },
		{ id: 'activity', label: 'Activity' }
	];
	const limitFields = [
		['max_rows_returned', 'Rows per result'],
		['max_result_bytes', 'Result bytes'],
		['max_query_runtime_seconds', 'Query runtime (s)'],
		['max_concurrent_queries', 'Concurrent queries'],
		['max_queries_per_hour', 'Queries per hour'],
		['max_tool_calls_per_minute', 'Tool calls per minute']
	] as const;

	const agentId = $derived(page.params.agent_id ?? '');
	const requestedTab = page.url.searchParams.get('tab') as Tab | null;
	let tab = $state<Tab>(
		requestedTab && tabs.some((t) => t.id === requestedTab) ? requestedTab : 'overview'
	);

	let agent = $state<Agent | null>(null);
	let tokens = $state<AgentToken[]>([]);
	let meta = $state<AgentsMeta | null>(null);
	let eligibility = $state<AgentCatalogEligibility[]>([]);
	let policyResponse = $state<AgentPolicyResponse | null>(null);
	let activity = $state<AuditEvent[]>([]);
	let catalogs = $state<Catalog[]>([]);
	let engines = $state<Engine[]>([]);

	let loading = $state(true);
	let error = $state('');

	let tokenOpen = $state(false);
	let tokenName = $state('');
	let tokenDays = $state('');
	let tokenBusy = $state(false);
	let tokenError = $state('');
	let secret = $state('');
	let secretOpen = $state(false);
	let acknowledged = $state(false);
	let revokeTarget = $state<AgentToken | null>(null);
	let revokeOpen = $state(false);

	let statusOpen = $state(false);
	let statusError = $state('');
	let deleteOpen = $state(false);
	let deleteName = $state('');
	let deleteError = $state('');

	let policy = $state({
		tools: [] as string[],
		allCatalogs: true,
		catalogs: [] as string[],
		allEngines: true,
		engines: [] as string[],
		limits: {} as Record<string, number | undefined>
	});
	let policyBusy = $state(false);
	let policyError = $state('');
	let policySaved = $state(false);

	const isDisabled = $derived(agent?.status === 'disabled');
	const canManagePolicy = $derived(agent?.allowed_actions?.includes('manage_policy') ?? false);
	const mcpSnippet = $derived(
		meta && secret
			? JSON.stringify(
					{
						mcpServers: {
							baleia: {
								url: meta.mcp_url,
								headers: { Authorization: `Bearer ${secret}` }
							}
						}
					},
					null,
					2
				)
			: ''
	);

	async function load() {
		loading = true;
		error = '';
		try {
			const path = { agent_id: agentId };
			const [a, t, metaRes, e, p] = await Promise.all([
				client.GET('/agents/{agent_id}', { params: { path } }),
				client.GET('/agents/{agent_id}/tokens', { params: { path } }),
				client.GET('/agents/meta'),
				client.GET('/agents/{agent_id}/eligibility', { params: { path } }),
				client.GET('/agents/{agent_id}/policy', { params: { path } })
			]);
			if (a.error || !a.data) {
				error = apiErrorMessage(a.error, m.settings_agents_could_not_load_the_agent());
				return;
			}
			agent = a.data;
			tokens = t.data ?? [];
			meta = metaRes.data ?? null;
			eligibility = e.data ?? [];
			policyResponse = p.data ?? null;
			initPolicy();
		} catch {
			error = m.settings_agents_could_not_load_the_agent();
		} finally {
			loading = false;
		}
	}

	function initPolicy() {
		if (!policyResponse) return;
		const stored = policyResponse.stored;
		policy = {
			tools: stored.tools ?? policyResponse.policy.tools ?? [],
			allCatalogs: stored.catalogs == null,
			catalogs: stored.catalogs ?? [],
			allEngines: stored.engines == null,
			engines: stored.engines ?? [],
			limits: {
				max_rows_returned: stored.max_rows_returned,
				max_result_bytes: stored.max_result_bytes,
				max_query_runtime_seconds: stored.max_query_runtime_seconds,
				max_concurrent_queries: stored.max_concurrent_queries,
				max_queries_per_hour: stored.max_queries_per_hour,
				max_tool_calls_per_minute: stored.max_tool_calls_per_minute
			}
		};
	}

	onMount(load);

	async function openTab(next: Tab) {
		tab = next;
		if (next === 'policy' && catalogs.length === 0) {
			const [c, e] = await Promise.all([client.GET('/catalogs'), client.GET('/engines')]);
			catalogs = c.data ?? [];
			engines = e.data ?? [];
		}
		if (next === 'activity') {
			const { data } = await client.GET('/audit/events', {
				params: { query: { actor_id: agentId, limit: 100 } }
			});
			activity = data?.items ?? [];
		}
	}

	function toggleTool(name: string, checked: boolean) {
		policy.tools = checked ? [...policy.tools, name] : policy.tools.filter((tool) => tool !== name);
	}

	function toggleId(field: 'catalogs' | 'engines', id: string, checked: boolean) {
		const current = policy[field];
		policy[field] = checked ? [...current, id] : current.filter((value) => value !== id);
	}

	async function savePolicy() {
		policyBusy = true;
		policyError = '';
		policySaved = false;
		try {
			const body = {
				version: 1,
				tools: policy.tools,
				catalogs: policy.allCatalogs ? null : policy.catalogs,
				engines: policy.allEngines ? null : policy.engines,
				max_rows_returned: policy.limits.max_rows_returned,
				max_result_bytes: policy.limits.max_result_bytes,
				max_query_runtime_seconds: policy.limits.max_query_runtime_seconds,
				max_concurrent_queries: policy.limits.max_concurrent_queries,
				max_queries_per_hour: policy.limits.max_queries_per_hour,
				max_tool_calls_per_minute: policy.limits.max_tool_calls_per_minute
			} as AgentPolicy;
			const { data, error: err } = await client.PUT('/agents/{agent_id}/policy', {
				params: { path: { agent_id: agentId } },
				body
			});
			if (err || !data) {
				policyError = apiErrorMessage(err, m.settings_agents_could_not_save_the_policy());
				return;
			}
			policyResponse = data;
			initPolicy();
			policySaved = true;
			toasts.success('Policy saved.');
		} finally {
			policyBusy = false;
		}
	}

	async function createToken() {
		tokenBusy = true;
		tokenError = '';
		try {
			const { data, error: err } = await client.POST('/agents/{agent_id}/tokens', {
				params: { path: { agent_id: agentId } },
				body: { name: tokenName, expires_in_days: tokenDays ? Number(tokenDays) : undefined }
			});
			if (err || !data) {
				tokenError = apiErrorMessage(err, m.settings_agents_could_not_create_the_token());
				return;
			}
			tokenOpen = false;
			tokenName = '';
			tokenDays = '';
			secret = data.token;
			acknowledged = false;
			secretOpen = true;
			await load();
		} finally {
			tokenBusy = false;
		}
	}

	async function confirmRevoke() {
		if (!revokeTarget) return;
		const { error: err } = await client.DELETE('/agents/{agent_id}/tokens/{token_id}', {
			params: { path: { agent_id: agentId, token_id: revokeTarget.id } }
		});
		if (err) throw new Error(apiErrorMessage(err, 'Could not revoke the token.'));
		toasts.success('Token revoked.');
		await load();
	}

	async function setStatus(status: 'active' | 'disabled') {
		statusError = '';
		const { error: err } = await client.PATCH('/agents/{agent_id}', {
			params: { path: { agent_id: agentId } },
			body: { status }
		});
		if (err) {
			statusError = apiErrorMessage(err, m.settings_agents_could_not_change_the_status());
			return;
		}
		statusOpen = false;
		toasts.success(status === 'disabled' ? 'Agent disabled.' : 'Agent enabled.');
		await load();
	}

	async function confirmDelete() {
		deleteError = '';
		const { error: err } = await client.DELETE('/agents/{agent_id}', {
			params: { path: { agent_id: agentId } }
		});
		if (err) {
			deleteError = apiErrorMessage(err, m.settings_agents_could_not_delete_the_agent());
			return;
		}
		toasts.success('Agent deleted.');
		await goto(resolve('/settings/agents'));
	}

	function displayLimit(field: string): number | undefined {
		return (
			policy.limits[field] ??
			(policyResponse?.policy[field as keyof AgentPolicy] as number | undefined)
		);
	}

	function ceiling(field: string): number | undefined {
		return policyResponse?.ceilings[field as keyof typeof policyResponse.ceilings] as
			number | undefined;
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-4xl">
		{#if loading}
			<div class="flex flex-col gap-3">
				<Skeleton height="2rem" />
				<Skeleton height="8rem" />
			</div>
		{:else if error}
			<ErrorState title="Could not load the agent" message={error} onretry={load} />
		{:else if agent}
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div>
					<div class="flex items-center gap-2">
						<h1 class="text-xl font-semibold tracking-[-0.3px]">{agent.name}</h1>
						<Badge tone={isDisabled ? 'neutral' : 'success'}>{agent.status}</Badge>
					</div>
					{#if agent.description}
						<p class="mt-1 text-[13px] text-text-muted">{agent.description}</p>
					{/if}
					<p class="mt-1 text-[11px] text-text-muted">
						Created <RelativeTime at={agent.created_at} /> ·
						{agent.allowed_actions?.includes('manage_tokens')
							? m.settings_agents_you_own_this_agent()
							: m.settings_agents_admin_view()}
					</p>
				</div>
				<div class="flex gap-2">
					{#if agent.allowed_actions?.includes('update')}
						<Button size="sm" onclick={() => (statusOpen = true)}>
							{isDisabled ? 'Enable' : 'Disable'}
						</Button>
					{/if}
					{#if agent.allowed_actions?.includes('delete')}
						<Button size="sm" variant="danger" onclick={() => (deleteOpen = true)}>Delete</Button>
					{/if}
				</div>
			</div>

			<nav class="mt-5 flex gap-1 border-b border-border" aria-label="Agent sections">
				{#each tabs as item (item.id)}
					<button
						type="button"
						class="-mb-px border-b-2 px-3 py-2 text-[13px] font-medium {tab === item.id
							? 'border-primary text-text-main'
							: 'border-transparent text-text-muted hover:text-text-main'}"
						aria-current={tab === item.id ? 'page' : undefined}
						onclick={() => openTab(item.id)}
					>
						{item.label}
					</button>
				{/each}
			</nav>

			{#if tab === 'overview'}
				<section class="mt-5 flex flex-col gap-4">
					<div class="rounded-lg border border-border bg-bg-panel p-4">
						<h2 class="text-[13px] font-semibold">Workspaces</h2>
						<p class="mt-1 text-[13px] text-text-muted">
							{agent.workspaces.map((ws) => ws.name).join(', ') ||
								m.settings_agents_no_workspace_bound()}
						</p>
					</div>
					<div class="rounded-lg border border-border bg-bg-panel p-4">
						<h2 class="text-[13px] font-semibold">Where it can run</h2>
						{#if eligibility.length === 0}
							<p class="mt-1 text-[13px] text-text-muted">
								No catalog is granted to the bound workspaces yet.
							</p>
						{:else}
							<div class="mt-3 flex flex-col gap-3">
								{#each eligibility as entry (entry.catalog_id)}
									<div>
										<div class="text-[12px] font-medium">{entry.catalog_name}</div>
										<ul class="mt-1 flex flex-col gap-1">
											{#each entry.engines as engine (engine.engine_id)}
												<li class="flex flex-wrap items-center gap-2 text-[12px]">
													<span>{engine.engine_name}</span>
													<span class="text-text-muted">({engine.kind})</span>
													{#if engine.status === 'ok'}
														<Badge tone="success">ok</Badge>
													{:else if engine.status === 'read_only_not_enforced'}
														<Badge tone="warning">read-only not enforced</Badge>
														<span class="text-text-muted">
															Agents cannot query it until an administrator asserts read-only
															credentials.
														</span>
													{:else}
														<Badge tone="neutral">excluded by policy</Badge>
													{/if}
												</li>
											{/each}
											{#if entry.engines.length === 0}
												<li class="text-[12px] text-text-muted">No engine hosts this catalog.</li>
											{/if}
										</ul>
									</div>
								{/each}
							</div>
						{/if}
					</div>
				</section>
			{:else if tab === 'tokens'}
				<section class="mt-5">
					<div class="flex items-center justify-between gap-3">
						<p class="text-[13px] text-text-muted">
							Tokens are shown once. Baleia stores only a hash; revoking one takes effect
							immediately.
						</p>
						{#if agent.allowed_actions?.includes('manage_tokens')}
							<Button variant="primary" size="sm" onclick={() => (tokenOpen = true)}>
								Create token
							</Button>
						{/if}
					</div>
					{#if tokens.length === 0}
						<p
							class="mt-4 rounded-lg border border-border bg-bg-panel p-4 text-[13px] text-text-muted"
						>
							No tokens yet.
						</p>
					{:else}
						<ul class="mt-4 flex flex-col gap-2">
							{#each tokens as token (token.id)}
								<li
									class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-bg-panel p-3 text-[13px]"
								>
									<div>
										<span class="font-medium">{token.name}</span>
										<span class="ml-2 font-mono text-[11px] text-text-muted">{token.prefix}</span>
										<div class="text-[11px] text-text-muted">
											Created <RelativeTime at={token.created_at} /> · expires
											<RelativeTime at={token.expires_at} />
											{#if token.last_used_at}
												· last used <RelativeTime at={token.last_used_at} />
											{/if}
										</div>
									</div>
									{#if !token.revoked_at && agent.allowed_actions?.includes('manage_tokens')}
										<Button
											size="sm"
											variant="ghost"
											onclick={() => {
												revokeTarget = token;
												revokeOpen = true;
											}}
										>
											Revoke
										</Button>
									{:else if token.revoked_at}
										<Badge tone="neutral">revoked</Badge>
									{/if}
								</li>
							{/each}
						</ul>
					{/if}
				</section>
			{:else if tab === 'policy'}
				<section class="mt-5 flex flex-col gap-4">
					{#if policyError}
						<p
							class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger"
						>
							{policyError}
						</p>
					{/if}
					<div class="rounded-lg border border-border bg-bg-panel p-4">
						<h2 class="text-[13px] font-semibold">Tools</h2>
						<p class="mt-1 text-[12px] text-text-muted">
							Agents are always read-only. “Can change state” tools are the only exception.
						</p>
						<div class="mt-3 grid gap-2 sm:grid-cols-2">
							{#each meta?.tools ?? [] as tool (tool.name)}
								<div class="flex items-start gap-2">
									<Checkbox
										checked={policy.tools.includes(tool.name)}
										label={tool.label}
										onchange={(event) =>
											toggleTool(tool.name, (event.currentTarget as HTMLInputElement).checked)}
									/>
									{#if !tool.read_only}
										<Badge tone="warning">can change state</Badge>
									{/if}
								</div>
							{/each}
						</div>
					</div>

					<div class="rounded-lg border border-border bg-bg-panel p-4">
						<h2 class="text-[13px] font-semibold">Catalogs</h2>
						<div class="mt-2 flex flex-col gap-2">
							<Checkbox
								checked={policy.allCatalogs}
								label="All catalogs granted to the bound workspaces"
								onchange={(event) => {
									policy.allCatalogs = (event.currentTarget as HTMLInputElement).checked;
								}}
							/>
							{#if !policy.allCatalogs}
								<div class="ml-5 grid gap-2 sm:grid-cols-2">
									{#each catalogs as catalog (catalog.id)}
										{#if catalog.id}
											<Checkbox
												checked={policy.catalogs.includes(catalog.id)}
												label={catalog.name ?? catalog.id}
												onchange={(event) =>
													toggleId(
														'catalogs',
														catalog.id ?? '',
														(event.currentTarget as HTMLInputElement).checked
													)}
											/>
										{/if}
									{/each}
								</div>
							{/if}
						</div>
					</div>

					<div class="rounded-lg border border-border bg-bg-panel p-4">
						<h2 class="text-[13px] font-semibold">Engines</h2>
						<div class="mt-2 flex flex-col gap-2">
							<Checkbox
								checked={policy.allEngines}
								label="Any engine hosting those catalogs"
								onchange={(event) => {
									policy.allEngines = (event.currentTarget as HTMLInputElement).checked;
								}}
							/>
							{#if !policy.allEngines}
								<div class="ml-5 grid gap-2 sm:grid-cols-2">
									{#each engines as engine (engine.id)}
										<Checkbox
											checked={policy.engines.includes(engine.id)}
											label={`${engine.name} (${engine.kind})`}
											onchange={(event) =>
												toggleId(
													'engines',
													engine.id,
													(event.currentTarget as HTMLInputElement).checked
												)}
										/>
									{/each}
								</div>
							{/if}
						</div>
					</div>

					<div class="rounded-lg border border-border bg-bg-panel p-4">
						<h2 class="text-[13px] font-semibold">Limits</h2>
						<p class="mt-1 text-[12px] text-text-muted">
							Empty keeps the default. The server maximum is enforced even if a value is larger.
						</p>
						<div class="mt-3 grid gap-3 sm:grid-cols-2">
							{#each limitFields as [field, label] (field)}
								<Input
									label={`${label} (max ${ceiling(field) ?? '—'})`}
									type="number"
									placeholder={displayLimit(field)?.toString() ??
										m.connections_providerform_default()}
									value={policy.limits[field]?.toString() ?? ''}
									oninput={(event) => {
										const value = (event.currentTarget as HTMLInputElement).value;
										policy.limits[field] = value === '' ? undefined : Number(value);
									}}
								/>
							{/each}
						</div>
					</div>

					<div class="flex items-center gap-2">
						<Button
							variant="primary"
							loading={policyBusy}
							disabled={!canManagePolicy}
							onclick={savePolicy}
						>
							Save policy
						</Button>
						{#if policySaved}<span class="text-[12px] text-text-muted">Saved.</span>{/if}
					</div>
				</section>
			{:else if tab === 'activity'}
				<section class="mt-5">
					{#if activity.length === 0}
						<p class="rounded-lg border border-border bg-bg-panel p-4 text-[13px] text-text-muted">
							No activity yet.
						</p>
					{:else}
						<ul class="flex flex-col gap-2">
							{#each activity as event (event.id)}
								<li class="rounded-lg border border-border bg-bg-panel p-3 text-[12px]">
									<div class="flex flex-wrap items-center gap-2">
										<span class="font-mono text-[11px]">{event.action}</span>
										<Badge tone={event.outcome === 'allowed' ? 'success' : 'warning'}>
											{event.outcome}
										</Badge>
										<span class="text-text-muted"><RelativeTime at={event.occurred_at} /></span>
									</div>
									{#if event.resource_id}
										<div class="mt-1 font-mono text-[11px] text-text-muted">
											{event.resource_type}
											{event.resource_id}
										</div>
									{/if}
								</li>
							{/each}
						</ul>
					{/if}
				</section>
			{/if}
		{/if}
	</div>
</div>

<Modal bind:open={tokenOpen} title={m.settings_agents_create_token()}>
	<div class="flex flex-col gap-3">
		{#if tokenError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{tokenError}
			</p>
		{/if}
		<Input label={m.common_name()} bind:value={tokenName} placeholder={m.common_ci_pipeline()} />
		<Input
			label={m.settings_agents_expires_in_days()}
			type="number"
			bind:value={tokenDays}
			placeholder={`Default ${meta?.token_default_ttl_days ?? 30}`}
		/>
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (tokenOpen = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={tokenBusy} onclick={createToken}
				>{m.settings_agents_create_token()}</Button
			>
		</div>
	{/snippet}
</Modal>

<Modal bind:open={secretOpen} title={m.settings_agents_copy_this_token_now()}>
	<div class="flex flex-col gap-3 text-[13px]">
		<p class="text-text-muted">{m.common_this_value_is_shown_once_and_is_never_stor()}</p>
		<div
			class="flex items-center gap-2 rounded-md border border-border bg-bg-toolbar px-3 py-2 font-mono text-xs"
		>
			<span class="flex-1 break-all">{secret}</span>
			<CopyButton value={secret} label={m.settings_agents_token()} />
		</div>
		<div>
			<div class="flex items-center justify-between">
				<span class="text-[12px] font-medium">{m.settings_agents_mcp_client_snippet()}</span>
				<CopyButton value={mcpSnippet} label={m.settings_agents_snippet()} />
			</div>
			<pre
				class="mt-1 overflow-auto rounded-md border border-border bg-bg-toolbar p-3 text-[11px]">{mcpSnippet}</pre>
		</div>
		<Checkbox bind:checked={acknowledged} label={m.common_i_have_copied_this_value()} />
	</div>
	{#snippet footer()}
		<Button
			variant="primary"
			disabled={!acknowledged}
			onclick={() => {
				secretOpen = false;
				secret = '';
			}}
		>
			{m.common_done()}
		</Button>
	{/snippet}
</Modal>

<ConfirmDialog
	bind:open={revokeOpen}
	title={m.settings_agents_revoke_token()}
	resourceName={revokeTarget?.name ?? ''}
	consequences={[
		'Clients using this token stop working immediately.',
		m.settings_agents_the_token_cannot_be_restored_create_a_new()
	]}
	confirmLabel="Revoke token"
	onconfirm={confirmRevoke}
/>

<Modal
	bind:open={statusOpen}
	title={isDisabled ? m.settings_agents_enable_agent() : m.settings_agents_disable_agent()}
>
	<div class="flex flex-col gap-3 text-[13px]">
		{#if statusError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{statusError}
			</p>
		{/if}
		<p class="text-text-muted">
			{isDisabled
				? m.settings_agents_the_agent_and_its_tokens_can_be_used_again()
				: m.settings_agents_every_token_stops_working_immediately_the()}
		</p>
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (statusOpen = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" onclick={() => setStatus(isDisabled ? 'active' : 'disabled')}>
				{isDisabled ? 'Enable' : 'Disable'}
			</Button>
		</div>
	{/snippet}
</Modal>

<Modal bind:open={deleteOpen} title={m.settings_agents_delete_agent()}>
	<div class="flex flex-col gap-3 text-[13px]">
		{#if deleteError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{deleteError}
			</p>
		{/if}
		<p class="text-text-muted">
			{m.settings_agents_tokens_stop_working_immediately_and_the_ag()}
			<strong>{agent?.name}</strong>
			{m.settings_agents_to_confirm()}
		</p>
		<Input label={m.settings_agents_agent_name()} bind:value={deleteName} />
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (deleteOpen = false)}>{m.common_cancel()}</Button>
			<Button variant="danger" disabled={deleteName !== agent?.name} onclick={confirmDelete}>
				{m.settings_agents_delete_agent()}
			</Button>
		</div>
	{/snippet}
</Modal>
