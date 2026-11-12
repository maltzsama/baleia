<script lang="ts">
	import { untrack } from 'svelte';
	import { SvelteMap } from 'svelte/reactivity';
	import { client } from '$lib/api/client';
	import type { components } from '$lib/api/schema';
	import type { Workspace } from '$lib/stores/workspace.svelte';
	import { catalogAccessStore } from '$lib/stores/catalogAccess.svelte';
	import { m } from '$lib/paraglide/messages';
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import KindIcon from '$lib/ui/patterns/KindIcon.svelte';

	type Grant = components['schemas']['CatalogGrantItem'];
	type Choice = {
		id: string;
		name: string;
		kind?: string | null;
		connection?: string | null;
		canAdd: boolean;
	};
	let { workspace, onchange }: { workspace: Workspace; onchange?: () => void } = $props();
	const workspaceId = $derived(workspace.id);
	const canManage = $derived(!!workspace.allowed_actions?.includes('manage_grants'));
	let choices = $state<Choice[]>([]);
	let grants = $state<Grant[]>([]);
	let loading = $state(true);
	let error = $state<unknown>(null);
	let busy = $state('');
	let search = $state('');
	let generation = 0;
	const filtered = $derived(
		choices.filter((choice) =>
			`${choice.name} ${choice.connection ?? ''}`
				.toLocaleLowerCase()
				.includes(search.trim().toLocaleLowerCase())
		)
	);

	async function load(id: string) {
		const version = ++generation;
		loading = true;
		error = null;
		try {
			const [access, requestable] = await Promise.all([
				client.GET('/catalog-grants', { params: { query: { workspace_id: id } } }),
				client.GET('/workspaces/{id}/requestable-catalogs', { params: { path: { id } } })
			]);
			if (access.error) throw access.error;
			if (requestable.error) throw requestable.error;
			const available = new SvelteMap<string, Choice>();
			for (const catalog of requestable.data ?? []) {
				available.set(catalog.id, {
					id: catalog.id,
					name: catalog.name,
					connection: catalog.connection_name,
					canAdd: false
				});
			}
			// Grantable catalogs are paginated; do not silently hide choices after the first page.
			for (let offset = 0; ; offset += 200) {
				const result = await client.GET('/catalogs', {
					params: { query: { action: 'grant', limit: 200, offset } }
				});
				if (result.error) throw result.error;
				for (const catalog of result.data ?? []) {
					if (!catalog.id) continue;
					available.set(catalog.id, {
						id: catalog.id,
						name: catalog.name ?? catalog.id,
						kind: catalog.provider_kind,
						connection: catalog.connection?.name,
						canAdd: !!catalog.allowed_actions?.includes('grant')
					});
				}
				if ((result.data?.length ?? 0) < 200) break;
			}
			for (const grant of access.data ?? []) {
				if (!available.has(grant.catalog_id))
					available.set(grant.catalog_id, {
						id: grant.catalog_id,
						name: grant.catalog_name ?? grant.catalog_id,
						connection: grant.connection_name,
						canAdd: false
					});
			}
			if (version !== generation) return;
			grants = access.data ?? [];
			choices = [...available.values()].sort((a, b) => a.name.localeCompare(b.name));
		} catch (cause) {
			if (version === generation) error = cause;
		} finally {
			if (version === generation) loading = false;
		}
	}

	async function add(choice: Choice, approve = false) {
		if (
			!canManage ||
			busy ||
			grants.some((item) => item.catalog_id === choice.id && (!approve || item.state === 'active'))
		)
			return;
		const id = workspaceId;
		const version = generation;
		busy = choice.id;
		error = null;
		try {
			const result = approve
				? await client.POST('/catalog-grants/{workspace_id}/{catalog_id}/approve', {
						params: { path: { workspace_id: id, catalog_id: choice.id } }
					})
				: await client.POST('/catalog-grants', {
						body: { workspace_id: id, catalog_id: choice.id }
					});
			if (result.error) throw result.error;
			if (!result.data) throw new Error('Missing catalog grant response');
			if (result.data.state === 'active') catalogAccessStore.changed();
			if (version !== generation) return;
			grants = [...grants.filter((item) => item.catalog_id !== choice.id), result.data];
			onchange?.();
		} catch (cause) {
			if (version === generation) error = cause;
		} finally {
			if (version === generation) busy = '';
		}
	}

	$effect(() => {
		const id = workspaceId;
		const allowed = canManage;
		choices = [];
		grants = [];
		search = '';
		busy = '';
		if (allowed) void untrack(() => load(id));
		else loading = false;
		return () => {
			generation++;
		};
	});
</script>

<div class="space-y-4">
	<p class="text-sm text-text-muted">{m.workspace_catalogs_context({ name: workspace.name })}</p>
	{#if !canManage}
		<EmptyState
			title={m.workspace_catalogs_no_permission()}
			description={m.workspace_catalogs_ask_admin()}
		/>
	{:else}
		<Input label={m.workspace_catalogs_search()} bind:value={search} />
		{#if error}
			<ErrorPanel {error} />
			<Button size="sm" disabled={!!busy} onclick={() => load(workspace.id)}
				>{m.common_retry()}</Button
			>
		{/if}
		{#if loading}
			<div role="status" aria-label={m.catalogs_loading_catalogs()} class="space-y-2">
				<Skeleton class="h-14 w-full" /><Skeleton class="h-14 w-full" />
			</div>
		{:else if !error || choices.length > 0}
			<ul class="space-y-2" aria-label={m.workspace_catalogs_title()}>
				{#each filtered as choice (choice.id)}
					{@const grant = grants.find((item) => item.catalog_id === choice.id)}
					<li class="flex flex-wrap items-center gap-3 rounded-lg border border-border p-3">
						<KindIcon kind={choice.kind ?? ''} family="catalog" size={24} />
						<div class="min-w-0 flex-1">
							<p class="break-words text-sm font-semibold">{choice.name}</p>
							{#if choice.connection}<p class="break-words text-xs text-text-muted">
									{choice.connection}
								</p>{/if}
						</div>
						{#if grant?.state === 'active'}
							<Badge tone="success">{m.workspace_catalogs_available()}</Badge>
						{:else if grant?.state === 'pending'}
							<Badge tone="warning"
								>{grant.initiated_by_side === 'catalog'
									? m.workspace_catalogs_offer()
									: m.workspace_catalogs_pending()}</Badge
							>
							{#if grant.initiated_by_side === 'catalog' && grant.allowed_actions?.includes('approve')}
								<Button
									size="sm"
									disabled={!!busy}
									loading={busy === choice.id}
									onclick={() => add(choice, true)}>{m.settings_workspaces_detail_approve()}</Button
								>
							{/if}
						{:else}
							<Button
								size="sm"
								variant="primary"
								disabled={!!busy}
								loading={busy === choice.id}
								onclick={() => add(choice)}
							>
								{choice.canAdd ? m.workspace_catalogs_add() : m.workspace_catalogs_request()}
							</Button>
						{/if}
					</li>
				{:else}
					<li>
						<EmptyState
							title={m.workspace_catalogs_empty()}
							description={m.workspace_catalogs_empty_hint()}
						/>
					</li>
				{/each}
			</ul>
		{/if}
	{/if}
</div>
