<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import Badge from '$lib/ui/Badge.svelte';
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
		<div class="flex flex-col gap-2" aria-label={m.catalogs_loading_catalogs()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title="Could not load catalogs" message={error} onretry={load} />
	{:else if catalogs.length === 0}
		<EmptyState title="No catalogs" description="No logical catalog matches these filters." />
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[11px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">Catalog</th>
						<th class="px-3 py-2">Origin</th>
						<th class="px-3 py-2">Provider</th>
						<th class="px-3 py-2">Connection</th>
						<th class="px-3 py-2">Engines</th>
						<th class="px-3 py-2">Metadata</th>
						<th class="px-3 py-2">Attention</th>
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
									<span class="text-slate-600 dark:text-slate-300">Never</span>
								{/if}
								{#if catalog.metadata_sync_status}
									<Badge
										tone={catalog.metadata_sync_status === 'failed'
											? 'danger'
											: catalog.metadata_sync_status === 'running'
												? 'info'
												: 'success'}
									>
										{catalog.metadata_sync_status}
									</Badge>
								{/if}
							</td>
							<td class="px-3 py-2">
								{#if catalog.needs_attention}
									<Badge tone="warning">Needs attention</Badge>
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
