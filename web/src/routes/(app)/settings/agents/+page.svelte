<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { canGlobal } from '$lib/authz/can';
	import { authStore } from '$lib/stores/auth.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import DataTable from '$lib/ui/patterns/DataTable.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	type Agent = components['schemas']['Agent'];
	type AgentWorkspace = components['schemas']['AgentWorkspace'];
	type Workspace = components['schemas']['Workspace'];

	let agents = $state<Agent[]>([]);
	let workspaces = $state<Workspace[]>([]);
	let loading = $state(true);
	let error = $state('');
	let showAll = $state(false);

	let createOpen = $state(false);
	let name = $state('');
	let description = $state('');
	let selectedWorkspaces = $state<string[]>([]);
	let createBusy = $state(false);
	let createError = $state('');

	const isAdmin = $derived(canGlobal(authStore.user, 'manage_users'));

	async function load() {
		loading = true;
		error = '';
		try {
			const [agentRes, wsRes] = await Promise.all([
				client.GET('/agents', { params: { query: { all: showAll } } }),
				client.GET('/workspaces')
			]);
			if (agentRes.error || !agentRes.data) {
				error = apiErrorMessage(agentRes.error, m.settings_agents_could_not_load_agents());
				return;
			}
			agents = agentRes.data;
			workspaces = wsRes.data ?? [];
		} catch {
			error = m.settings_agents_could_not_load_agents();
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function toggleAll(checked: boolean) {
		showAll = checked;
		await load();
	}

	function toggleWorkspace(id: string, checked: boolean) {
		selectedWorkspaces = checked
			? [...selectedWorkspaces, id]
			: selectedWorkspaces.filter((workspace) => workspace !== id);
	}

	function openCreate() {
		name = '';
		description = '';
		selectedWorkspaces = [];
		createError = '';
		createOpen = true;
	}

	async function createAgent() {
		createBusy = true;
		createError = '';
		try {
			const { data, error: err } = await client.POST('/agents', {
				body: {
					name,
					description,
					workspace_ids: selectedWorkspaces
				}
			});
			if (err || !data) {
				createError = apiErrorMessage(err, m.settings_agents_could_not_create_the_agent());
				return;
			}
			createOpen = false;
			toasts.success('Agent created. Create its first token next.');
			await load();
		} finally {
			createBusy = false;
		}
	}

	const rows = $derived(agents.map((agent) => ({ ...agent, key: agent.id })));

	const columns = [
		{ key: 'name', label: m.common_name(), sortable: true },
		{ key: 'status', label: m.history_status() },
		{ key: 'workspaces', label: m.settings_agents_workspaces() },
		{ key: 'tokens', label: 'Tokens' },
		{ key: 'last_used', label: m.settings_agents_last_used() },
		{ key: 'created', label: m.settings_agents_created() }
	];
</script>

<div class="flex-1 overflow-y-auto p-7">
	<!-- Lists and tables use the full width (#468). -->
	<div class="w-full">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.settings_agents_agents()}</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					{m.settings_agents_read_only_mcp_clients_that_act_in_your_wor()}
				</p>
			</div>
			<Button variant="primary" onclick={openCreate}>{m.settings_agents_new_agent()}</Button>
		</div>

		{#if isAdmin}
			<label class="mt-4 inline-flex items-center gap-2 text-[12px] text-text-muted">
				<Checkbox
					checked={showAll}
					label={m.settings_agents_show_all_agents()}
					onchange={(event) => toggleAll((event.currentTarget as HTMLInputElement).checked)}
				/>
			</label>
		{/if}

		{#if loading}
			<div class="mt-6 flex flex-col gap-3">
				<Skeleton height="5rem" />
				<Skeleton height="5rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState title="Could not load agents" message={error} onretry={load} />
			</div>
		{:else if agents.length === 0}
			<div class="mt-6">
				<EmptyState
					title="No agents"
					description="Create an agent to give a script or an MCP client read-only access to your workspaces."
				>
					<Button variant="primary" onclick={openCreate}>New agent</Button>
				</EmptyState>
			</div>
		{:else}
			<div class="mt-6">
				<DataTable {columns} {rows} rowKey={(row) => String(row.key)}>
					{#snippet cell(row, column)}
						{#if column.key === 'name'}
							<a class="text-primary hover:underline" href={resolve(`/settings/agents/${row.id}`)}>
								{String(row.name)}
							</a>
						{:else if column.key === 'status'}
							<Badge tone={row.status === 'active' ? 'success' : 'neutral'}>
								{String(row.status)}
							</Badge>
						{:else if column.key === 'workspaces'}
							{(row.workspaces as AgentWorkspace[]).map((ws) => ws.name).join(', ') || '—'}
						{:else if column.key === 'tokens'}
							{String(row.active_token_count ?? 0)}
						{:else if column.key === 'last_used'}
							{row.last_used_at
								? new Date(String(row.last_used_at)).toLocaleString()
								: m.settings_agents_never()}
						{:else if column.key === 'created'}
							<RelativeTime at={String(row.created_at)} />
						{/if}
					{/snippet}
				</DataTable>
			</div>
		{/if}
	</div>
</div>

<Modal bind:open={createOpen} title={m.settings_agents_new_agent()}>
	<div class="flex flex-col gap-3">
		{#if createError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{createError}
			</p>
		{/if}
		<Input
			label={m.common_name()}
			bind:value={name}
			placeholder={m.settings_agents_etl_pipeline()}
		/>
		<Input
			label={m.common_description()}
			bind:value={description}
			placeholder={m.common_optional()}
		/>
		<fieldset class="flex flex-col gap-2">
			<legend class="text-[12px] font-medium text-text-muted"
				>{m.settings_agents_workspaces()}</legend
			>
			{#each workspaces as workspace (workspace.id)}
				<Checkbox
					checked={selectedWorkspaces.includes(workspace.id)}
					label={workspace.name}
					onchange={(event) =>
						toggleWorkspace(workspace.id, (event.currentTarget as HTMLInputElement).checked)}
				/>
			{/each}
			{#if workspaces.length === 0}
				<p class="text-[12px] text-text-muted">{m.settings_agents_you_have_no_workspaces_yet()}</p>
			{/if}
		</fieldset>
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (createOpen = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={createBusy} onclick={createAgent}
				>{m.settings_agents_create_agent()}</Button
			>
		</div>
	{/snippet}
</Modal>
