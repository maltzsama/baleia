<script lang="ts">
	import { onMount } from 'svelte';
	import { SvelteURLSearchParams } from 'svelte/reactivity';
	import { authStore } from '$lib/stores/auth.svelte';
	import { canGlobal } from '$lib/authz/can';
	import {
		downloadAuditCsv,
		listAuditActions,
		listAuditEvents,
		resolveAudit,
		type AuditEvent,
		type AuditFilters
	} from '$lib/audit/client';
	import { toasts } from '$lib/ui/toast.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Drawer from '$lib/ui/Drawer.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	type Names = {
		users: Record<string, { name: string; email?: string }>;
		workspaces: Record<string, { name: string }>;
		connections: Record<string, { name: string }>;
		catalogs: Record<string, { name: string }>;
	};

	const ranges = [
		{ value: '1h', label: 'Last hour' },
		{ value: '24h', label: 'Last 24 hours' },
		{ value: '7d', label: 'Last 7 days' },
		{ value: '30d', label: 'Last 30 days' }
	];

	let range = $state('7d');
	let actorId = $state('');
	let action = $state('');
	let resourceType = $state('');
	let resourceId = $state('');
	let outcome = $state('');
	let requestId = $state('');
	let actions = $state<string[]>([]);
	let events = $state<AuditEvent[]>([]);
	let names = $state<Names>({ users: {}, workspaces: {}, connections: {}, catalogs: {} });
	let nextCursor = $state<string | null>(null);
	let loading = $state(true);
	let loadingMore = $state(false);
	let error = $state('');
	let selected = $state<AuditEvent | null>(null);
	let drawerOpen = $state(false);

	const isAdmin = $derived(canGlobal(authStore.user, 'view_all_audit'));

	function rangeMs(value: string): number {
		switch (value) {
			case '1h':
				return 3600_000;
			case '24h':
				return 24 * 3600_000;
			case '30d':
				return 30 * 24 * 3600_000;
			default:
				return 7 * 24 * 3600_000;
		}
	}

	function filters(cursor?: string): AuditFilters {
		return {
			from: new Date(Date.now() - rangeMs(range)).toISOString(),
			actorId: actorId.trim() || undefined,
			action: action || undefined,
			resourceType: resourceType.trim() || undefined,
			resourceId: resourceId.trim() || undefined,
			outcome: outcome || undefined,
			requestId: requestId.trim() || undefined,
			limit: 50,
			cursor
		};
	}

	function syncUrl() {
		if (typeof window === 'undefined') return;
		const params = new SvelteURLSearchParams();
		if (resourceType) params.set('resource_type', resourceType);
		if (resourceId) params.set('resource_id', resourceId);
		if (action) params.set('action', action);
		if (outcome) params.set('outcome', outcome);
		const query = params.toString();
		window.history.replaceState(null, '', `${window.location.pathname}${query ? `?${query}` : ''}`);
	}

	async function resolveNames(items: AuditEvent[]) {
		const unique = (values: (string | null | undefined)[]) => [
			...new Set(values.filter((value): value is string => !!value))
		];
		const users = unique(items.map((item) => item.actor_id));
		const workspaces = unique(items.map((item) => item.workspace_id));
		const connections = unique(
			items.filter((item) => item.resource_type === 'connection').map((item) => item.resource_id)
		);
		const catalogs = unique(
			items.filter((item) => item.resource_type === 'catalog').map((item) => item.resource_id)
		);
		if (!users.length && !workspaces.length && !connections.length && !catalogs.length) return;
		try {
			const resolved = await resolveAudit({ users, workspaces, connections, catalogs });
			names = {
				users: { ...names.users, ...(resolved.users ?? {}) },
				workspaces: { ...names.workspaces, ...(resolved.workspaces ?? {}) },
				connections: { ...names.connections, ...(resolved.connections ?? {}) },
				catalogs: { ...names.catalogs, ...(resolved.catalogs ?? {}) }
			};
		} catch {
			// Names are a convenience; raw ids remain visible.
		}
	}

	async function load() {
		loading = true;
		error = '';
		try {
			const page = await listAuditEvents(filters());
			events = page.items;
			nextCursor = page.next_cursor ?? null;
			syncUrl();
			await resolveNames(page.items);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function loadMore() {
		if (!nextCursor) return;
		loadingMore = true;
		try {
			const page = await listAuditEvents(filters(nextCursor));
			events = [...events, ...page.items];
			nextCursor = page.next_cursor ?? null;
			await resolveNames(page.items);
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			loadingMore = false;
		}
	}

	async function exportCsv() {
		try {
			const blob = await downloadAuditCsv(filters());
			const url = URL.createObjectURL(blob);
			const link = document.createElement('a');
			link.href = url;
			link.download = 'audit-events.csv';
			link.click();
			URL.revokeObjectURL(url);
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		}
	}

	function openEvent(event: AuditEvent) {
		selected = event;
		drawerOpen = true;
	}

	function actorLabel(event: AuditEvent): string {
		if (event.actor_id && names.users[event.actor_id]) return names.users[event.actor_id].name;
		return event.actor_id ?? event.actor_kind;
	}

	function workspaceLabel(event: AuditEvent): string {
		if (event.workspace_id && names.workspaces[event.workspace_id]) {
			return names.workspaces[event.workspace_id].name;
		}
		return event.workspace_id ?? '';
	}

	function resourceLabel(event: AuditEvent): string {
		if (!event.resource_id) return event.resource_type;
		const map =
			event.resource_type === 'connection'
				? names.connections
				: event.resource_type === 'catalog'
					? names.catalogs
					: {};
		const name = map[event.resource_id]?.name;
		return name
			? `${event.resource_type} · ${name}`
			: `${event.resource_type} · ${event.resource_id}`;
	}

	function outcomeTone(value: string): 'success' | 'danger' | 'warning' {
		return value === 'allowed' ? 'success' : value === 'denied' ? 'danger' : 'warning';
	}

	onMount(async () => {
		if (typeof window !== 'undefined') {
			const params = new SvelteURLSearchParams(window.location.search);
			resourceType = params.get('resource_type') ?? '';
			resourceId = params.get('resource_id') ?? '';
			action = params.get('action') ?? '';
			outcome = params.get('outcome') ?? '';
		}
		try {
			actions = await listAuditActions();
		} catch {
			actions = [];
		}
		await load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">Audit</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{isAdmin
					? 'Every security and data operation on this instance.'
					: 'Your events and the events of workspaces you own.'}
			</p>
		</div>
		{#if isAdmin}
			<Button onclick={exportCsv}>Export CSV</Button>
		{/if}
	</div>

	<div
		class="mb-4 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-4"
	>
		<Select label="Window" bind:value={range} options={ranges} onchange={() => load()} />
		<Select
			label="Action"
			bind:value={action}
			options={[
				{ value: '', label: 'Any' },
				...actions.map((item) => ({ value: item, label: item }))
			]}
		/>
		<Input
			label="Resource type"
			bind:value={resourceType}
			placeholder="connection"
			class="font-mono"
		/>
		<Select
			label="Outcome"
			bind:value={outcome}
			options={[
				{ value: '', label: 'Any' },
				{ value: 'allowed', label: 'Allowed' },
				{ value: 'denied', label: 'Denied' },
				{ value: 'error', label: 'Error' }
			]}
		/>
		{#if isAdmin}
			<Input label="Actor id" bind:value={actorId} placeholder="UUID" class="font-mono" />
		{/if}
		<Input label="Request id" bind:value={requestId} placeholder="request id" class="font-mono" />
		<div class="flex items-end gap-2">
			<Button variant="primary" {loading} onclick={load}>Search</Button>
			<Button
				onclick={() => {
					actorId = '';
					action = '';
					resourceType = '';
					resourceId = '';
					outcome = '';
					requestId = '';
					void load();
				}}
			>
				Clear
			</Button>
		</div>
	</div>

	{#if loading}
		<div class="flex flex-col gap-2" aria-label="Loading audit events">
			{#each [0, 1, 2, 3, 4] as item (item)}
				<Skeleton class="h-9 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title="Could not load the audit trail" message={error} onretry={load} />
	{:else if events.length === 0}
		<EmptyState
			title="No events"
			description="No audit event matches these filters in the selected window."
		/>
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[11px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">Time</th>
						<th class="px-3 py-2">Actor</th>
						<th class="px-3 py-2">Action</th>
						<th class="px-3 py-2">Resource</th>
						<th class="px-3 py-2">Workspace</th>
						<th class="px-3 py-2">Outcome</th>
						<th class="px-3 py-2">Reason</th>
					</tr>
				</thead>
				<tbody>
					{#each events as event (event.id)}
						<tr
							class="cursor-pointer border-b border-border/60 last:border-b-0 hover:bg-bg-toolbar"
							onclick={() => openEvent(event)}
							onkeydown={(e) => e.key === 'Enter' && openEvent(event)}
							tabindex="0"
						>
							<td class="whitespace-nowrap px-3 py-1.5"><RelativeTime at={event.occurred_at} /></td>
							<td class="px-3 py-1.5">{actorLabel(event)}</td>
							<td class="px-3 py-1.5 font-mono">{event.action}</td>
							<td class="px-3 py-1.5">{resourceLabel(event)}</td>
							<td class="px-3 py-1.5">{workspaceLabel(event)}</td>
							<td class="px-3 py-1.5">
								<Badge tone={outcomeTone(event.outcome)}>{event.outcome}</Badge>
							</td>
							<td class="px-3 py-1.5 text-slate-600 dark:text-slate-300">{event.reason ?? ''}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		{#if nextCursor}
			<div class="mt-3 flex justify-center">
				<Button loading={loadingMore} onclick={loadMore}>Load more</Button>
			</div>
		{/if}
	{/if}
</div>

<Drawer bind:open={drawerOpen} title="Audit event">
	{#if selected}
		<dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
			<dt class="text-slate-600 dark:text-slate-300">Time</dt>
			<dd><RelativeTime at={selected.occurred_at} live /></dd>
			<dt class="text-slate-600 dark:text-slate-300">Outcome</dt>
			<dd><Badge tone={outcomeTone(selected.outcome)}>{selected.outcome}</Badge></dd>
			<dt class="text-slate-600 dark:text-slate-300">Action</dt>
			<dd class="font-mono">{selected.action}</dd>
			<dt class="text-slate-600 dark:text-slate-300">Actor</dt>
			<dd>
				{actorLabel(selected)}
				<span class="text-slate-600 dark:text-slate-300">({selected.actor_kind})</span>
			</dd>
			<dt class="text-slate-600 dark:text-slate-300">Resource</dt>
			<dd>{resourceLabel(selected)}</dd>
			<dt class="text-slate-600 dark:text-slate-300">Workspace</dt>
			<dd>{workspaceLabel(selected)}</dd>
			{#if selected.reason}
				<dt class="text-slate-600 dark:text-slate-300">Reason</dt>
				<dd>{selected.reason}</dd>
			{/if}
			{#if selected.client_ip}
				<dt class="text-slate-600 dark:text-slate-300">Client IP</dt>
				<dd class="font-mono">{selected.client_ip}</dd>
			{/if}
			{#if selected.user_agent}
				<dt class="text-slate-600 dark:text-slate-300">User agent</dt>
				<dd class="break-all">{selected.user_agent}</dd>
			{/if}
			{#if selected.request_id}
				<dt class="text-slate-600 dark:text-slate-300">Request</dt>
				<dd class="flex items-center gap-1 break-all font-mono">
					{selected.request_id}<CopyButton value={selected.request_id} label="request id" />
				</dd>
			{/if}
			{#if selected.trace_id}
				<dt class="text-slate-600 dark:text-slate-300">Trace</dt>
				<dd class="flex items-center gap-1 break-all font-mono">
					{selected.trace_id}<CopyButton value={selected.trace_id} label="trace id" />
				</dd>
			{/if}
		</dl>
		{#if selected.changes && Object.keys(selected.changes).length > 0}
			<h3 class="mt-4 text-[12px] font-semibold">Changes</h3>
			<table class="mt-1 w-full text-left text-[11px]">
				<thead class="text-[10px] uppercase text-slate-600 dark:text-slate-300">
					<tr>
						<th class="py-1">Field</th>
						<th class="py-1">From</th>
						<th class="py-1">To</th>
					</tr>
				</thead>
				<tbody>
					{#each Object.entries(selected.changes) as [field, change] (field)}
						<tr class="border-t border-border/60">
							<td class="py-1 font-mono">{field}</td>
							<td class="py-1 font-mono">{JSON.stringify(change.from) ?? ''}</td>
							<td class="py-1 font-mono">{JSON.stringify(change.to) ?? ''}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		{/if}
	{/if}
</Drawer>
