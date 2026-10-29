<script lang="ts">
	import { resolve } from '$app/paths';
	import { m } from '$lib/paraglide/messages';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import {
		analyzeCatalogTableHealth,
		dataTableQuery,
		getCatalogMaintenancePlan,
		type CatalogMaintenancePlanItem
	} from './client';
	import { operationLabel, reasonText } from './labels';

	let { catalogId }: { catalogId: string } = $props();

	let loading = $state(true);
	let error = $state('');
	let items = $state<CatalogMaintenancePlanItem[]>([]);
	let analyzing = $state(false);

	async function load() {
		loading = true;
		error = '';
		try {
			items = await getCatalogMaintenancePlan(catalogId);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		void catalogId;
		void load();
	});

	async function analyze() {
		analyzing = true;
		try {
			await analyzeCatalogTableHealth(catalogId);
			toasts.success(m.maintenance_analysis_started());
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			analyzing = false;
		}
	}

	function modeLabel(mode: string): string {
		return mode === 'managed'
			? m.maintenance_mode_managed()
			: mode === 'off'
				? m.maintenance_mode_off()
				: m.maintenance_mode_elsewhere();
	}
</script>

<section aria-labelledby="catalog-maintenance-heading">
	<div class="mb-3 flex flex-wrap items-center justify-between gap-2">
		<div>
			<h2 id="catalog-maintenance-heading" class="text-[13px] font-semibold">
				{m.maintenance_catalog_plan()}
			</h2>
			<p class="text-[12px] text-text-muted">{m.maintenance_catalog_plan_hint()}</p>
		</div>
		<Button size="sm" loading={analyzing} onclick={analyze}>{m.maintenance_analyze_now()}</Button>
	</div>

	{#if loading}
		<div class="space-y-2" role="status" aria-label={m.maintenance_loading()}>
			<Skeleton class="h-8 w-full" />
			<Skeleton class="h-8 w-full" />
		</div>
	{:else if error}
		<ErrorState title={m.maintenance_could_not_load()} message={error} onretry={load} />
	{:else if items.length === 0}
		<EmptyState
			title={m.maintenance_no_parameters()}
			description={m.maintenance_no_parameters_hint()}
		>
			<a class="text-[12px] font-semibold text-primary" href={resolve('/data')}
				>{m.maintenance_go_to_data()}</a
			>
		</EmptyState>
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border">
			<table class="w-full text-left text-[12px]">
				<thead class="bg-bg-subtle text-[11px] uppercase tracking-wide text-text-muted">
					<tr>
						<th class="px-3 py-2">{m.maintenance_col_table()}</th>
						<th class="px-3 py-2">{m.maintenance_mode()}</th>
						<th class="px-3 py-2">{m.maintenance_col_next()}</th>
						<th class="px-3 py-2">{m.maintenance_col_last_run()}</th>
					</tr>
				</thead>
				<tbody>
					{#each items as item (`${item.namespace}.${item.table_name}`)}
						{@const next = item.plan.plan.find((step) => step.would_run)}
						{@const configureHref = `${resolve('/data')}?${dataTableQuery(catalogId, item.namespace, item.table_name)}`}
						<tr class="border-t border-border align-top">
							<td class="px-3 py-1.5 font-mono">
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- configureHref is resolve('/data') plus the query -->
								<a class="text-primary" href={configureHref}>{item.namespace}.{item.table_name}</a>
							</td>
							<td class="px-3 py-1.5"><Badge tone="neutral">{modeLabel(item.mode)}</Badge></td>
							<td class="px-3 py-1.5">
								{#if next}
									<span class="font-semibold">{operationLabel(next.operation)}</span>
									<span class="text-text-muted"> · {reasonText(next)}</span>
								{:else}
									<span class="text-text-muted">{m.maintenance_nothing_due()}</span>
								{/if}
							</td>
							<td class="px-3 py-1.5">
								{#if item.last_run}
									<span class="inline-flex items-center gap-1.5">
										<StatusBadge kind="maintenance" status={item.last_run.status} />
										<RelativeTime at={item.last_run.started_at} />
									</span>
								{:else}
									<span class="text-text-muted">{m.maintenance_no_runs()}</span>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</section>
