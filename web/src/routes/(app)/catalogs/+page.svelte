<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Button from '$lib/ui/Button.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { canGlobal } from '$lib/authz/can';
	import { onMount } from 'svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import EngineBadge from '$lib/ui/patterns/EngineBadge.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import { listCatalogs, type CatalogSummary } from '$lib/catalogs/admin';
	import { listEngines, type Engine } from '$lib/engines/admin';

	let catalogs = $state<CatalogSummary[]>([]);
	let engines = $state<Engine[]>([]);
	let loading = $state(true);
	let error = $state('');
	let q = $state('');
	let origin = $state('');
	let engineId = $state('');
	let attention = $state(false);

	async function load() {
		loading = true;
		error = '';
		try {
			[catalogs, engines] = await Promise.all([
				listCatalogs({
					q: q.trim() || undefined,
					origin: origin || undefined,
					engine_id: engineId || undefined,
					attention: attention || undefined
				}),
				listEngines()
			]);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	// The list is filtered server-side; these say whether it was.
	const filtering = $derived(!!(q.trim() || origin || engineId || attention));

	function clearFilters() {
		q = '';
		origin = '';
		engineId = '';
		attention = false;
		void load();
	}

	function open(catalog: CatalogSummary) {
		if (catalog.id) window.location.href = `/catalogs/${catalog.id}`;
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_catalogs()}</h1>
		<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
			{m.catalogs_logical_catalogs_where_they_are_bound_and()}
		</p>
	</div>

	<div
		class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-4"
	>
		<Input label={m.common_search()} bind:value={q} placeholder={m.common_orders()} />
		<Select
			label={m.catalogs_origin()}
			bind:value={origin}
			options={[
				{ value: '', label: m.monitoring_any() },
				{ value: 'source', label: m.catalogs_source() },
				{ value: 'native', label: 'Native' }
			]}
		/>
		<Select
			label={m.common_engine()}
			bind:value={engineId}
			options={[
				{ value: '', label: m.monitoring_any() },
				...engines.map((engine) => ({ value: engine.id, label: engine.name }))
			]}
		/>
		<label class="flex items-end gap-2 pb-1 text-[12px]">
			<input type="checkbox" bind:checked={attention} onchange={() => load()} />
			{m.catalogs_needs_attention_only()}
		</label>
	</div>
	<div class="mb-3 flex justify-end">
		<button type="button" class="text-[12px] text-primary hover:underline" onclick={() => load()}>
			{m.catalogs_apply_filters()}
		</button>
	</div>

	{#if loading}
		<div class="flex flex-col gap-2" role="status" aria-label={m.catalogs_loading_catalogs()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title={m.catalogs_could_not_load_catalogs()} message={error} onretry={load} />
	{:else if catalogs.length === 0 && filtering}
		<div role="status">
			<EmptyState
				title={m.catalogs_no_catalog_matches_these_filters()}
				description={m.common_change_or_clear_the_filters()}
			>
				<Button onclick={clearFilters}>{m.common_clear_filters()}</Button>
			</EmptyState>
		</div>
	{:else if catalogs.length === 0}
		<!-- No catalog at all (#476): catalogs come from connections. -->
		<EmptyState
			title={m.catalogs_no_catalogs_yet()}
			description={m.catalogs_catalogs_come_from_connections()}
		>
			{#if canGlobal(authStore.user, 'create_connection')}
				<Button variant="primary" onclick={() => (window.location.href = '/connections')}>
					{m.catalogs_go_to_connections()}
				</Button>
			{/if}
		</EmptyState>
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[11px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">{m.catalogs_catalog()}</th>
						<th class="px-3 py-2">{m.catalogs_origin()}</th>
						<th class="px-3 py-2">{m.catalogs_provider()}</th>
						<th class="px-3 py-2">{m.catalogs_connection()}</th>
						<th class="px-3 py-2">{m.catalogs_engines()}</th>
						<th class="px-3 py-2">{m.catalogs_metadata()}</th>
						<th class="px-3 py-2">{m.catalogs_attention()}</th>
					</tr>
				</thead>
				<tbody>
					{#each catalogs as catalog (catalog.id)}
						<tr
							class="cursor-pointer border-b border-border/60 last:border-b-0 hover:bg-bg-toolbar"
							onclick={() => open(catalog)}
							onkeydown={(e) => e.key === 'Enter' && open(catalog)}
							tabindex="0"
						>
							<td class="px-3 py-2 font-medium">{catalog.name ?? catalog.id}</td>
							<td class="px-3 py-2"
								><Badge tone={catalog.origin === 'native' ? 'neutral' : 'info'}
									>{catalog.origin}</Badge
								></td
							>
							<td class="px-3 py-2">{catalog.provider_kind ?? '—'}</td>
							<td class="px-3 py-2">{catalog.connection?.name ?? '—'}</td>
							<td class="px-3 py-2">
								<div class="flex flex-wrap gap-1">
									{#each catalog.bindings as binding (binding.id)}
										<span class="inline-flex items-center gap-1">
											<EngineBadge engine={binding.engine} />
											<Badge
												tone={binding.sync_status === 'synced'
													? 'success'
													: binding.sync_status === 'error'
														? 'danger'
														: 'neutral'}
											>
												{binding.sync_status ?? 'pending'}
											</Badge>
										</span>
									{:else}
										<span class="text-slate-600 dark:text-slate-300">—</span>
									{/each}
								</div>
							</td>
							<td class="px-3 py-2">
								{#if catalog.metadata_synced_at}
									<RelativeTime at={catalog.metadata_synced_at} />
								{:else}
									<span class="text-slate-600 dark:text-slate-300">{m.catalogs_never()}</span>
								{/if}
								{#if catalog.metadata_sync_status}
									<StatusBadge kind="metadataSync" status={catalog.metadata_sync_status} />
								{/if}
							</td>
							<td class="px-3 py-2">
								{#if catalog.needs_attention}
									<Badge tone="warning">{m.catalogs_needs_attention()}</Badge>
								{:else}
									<span class="text-slate-600 dark:text-slate-300">—</span>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
