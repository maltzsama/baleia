<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { editor } from '$lib/stores/editor.svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { listCatalogBindings } from '$lib/connections/api';
	import { listModels, migratedModelOf } from '$lib/semantic/explore';
	import HistoryDrawer from '$lib/content/HistoryDrawer.svelte';
	import {
		deleteMetric,
		getMetric,
		metricSQL,
		updateMetric,
		type MetricDefinition,
		type SemanticMetric
	} from '$lib/metrics/client';
	import { describeDefinition } from '$lib/metrics/definition';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const metricId = $derived(page.params.metric_id ?? '');

	let metric = $state<SemanticMetric | null>(null);
	let loading = $state(true);
	let error = $state('');
	let bindings = $state<{ id: string; label: string }[]>([]);
	let bindingId = $state('');
	let grain = $state('');
	let sql = $state('');
	let portable = $state(true);
	let sqlError = $state('');
	let sqlLoading = $state(false);
	let historyOpen = $state(false);
	let deleteOpen = $state(false);
	let certifyOpen = $state(false);

	const definition = $derived((metric?.definition ?? {}) as unknown as MetricDefinition);
	const canCertify = $derived(!!metric?.allowed_actions?.includes('certify'));
	const canEdit = $derived(!!metric?.allowed_actions?.includes('update'));

	async function load() {
		if (!workspaceId || !metricId) return;
		loading = true;
		error = '';
		try {
			await savedQueries.reload(workspaceId);
			metric = await getMetric(workspaceId, metricId);
			// A migrated v1 metric lives on in its model (#91): keep old links working.
			const migrated = migratedModelOf(await listModels(workspaceId).catch(() => []), metric);
			if (migrated) {
				await goto(resolve(`/models/${migrated.id}`), {
					replaceState: true
				});
				return;
			}
			const rows = await listCatalogBindings(metric.catalog_id);
			bindings = rows
				.filter((b) => b.enabled)
				.map((b) => ({
					id: b.id,
					label: `${b.engine_catalog_name} (${b.connector ?? 'default'})`
				}));
			bindingId = metric.binding_id ?? bindings[0]?.id ?? '';
			grain = (definition.time_grains ?? [])[0] ?? '';
			await loadSQL();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function loadSQL() {
		if (!workspaceId || !metricId) return;
		sqlLoading = true;
		sqlError = '';
		try {
			const out = await metricSQL(workspaceId, metricId, {
				binding_id: bindingId || undefined,
				grain: (grain || undefined) as
					'day' | 'hour' | 'minute' | 'month' | 'second' | 'year' | 'quarter' | 'week' | undefined
			});
			sql = out.sql;
			portable = out.portable;
		} catch (e) {
			sql = '';
			sqlError = e instanceof Error ? e.message : String(e);
		} finally {
			sqlLoading = false;
		}
	}

	function openInEditor() {
		if (!sql || !metric) return;
		editor.addTab({ name: metric.label, sql, bindingId });
		window.location.href = '/sql';
	}

	async function setCertified(certified: boolean) {
		if (!metric) return;
		metric = await updateMetric(workspaceId, metric.id, { certified });
	}

	async function remove() {
		if (!metric) return;
		await deleteMetric(workspaceId, metric.id);
		window.location.href = '/metrics';
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	{#if loading && !metric}
		<Skeleton class="h-32 w-full" />
	{:else if error && !metric}
		<ErrorState title="Could not load the metric" message={error} onretry={load} />
	{:else if metric}
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div>
				<div class="flex flex-wrap items-center gap-2">
					<h1 class="text-xl font-semibold tracking-[-0.3px]">{metric.label}</h1>
					{#if metric.certified}
						<Badge tone="success">Certified</Badge>
					{/if}
					{#if !portable}
						<Badge tone="warning">Non-portable</Badge>
					{/if}
				</div>
				<p
					class="mt-1 flex items-center gap-1 font-mono text-[11px] text-slate-600 dark:text-slate-300"
				>
					{metric.slug}<CopyButton value={metric.slug} label="slug" />
					· {savedQueries.spaces.find((space) => space.id === metric?.space_id)?.name ?? '—'}
					· updated <RelativeTime at={metric.updated_at} />
				</p>
			</div>
			<div class="flex flex-wrap gap-2">
				{#if canCertify}
					<Button onclick={() => (metric?.certified ? (certifyOpen = true) : setCertified(true))}>
						{metric.certified ? m.metrics_remove_certification() : m.metrics_certify()}
					</Button>
				{/if}
				{#if canEdit}
					<Button onclick={() => (window.location.href = `/metrics/${metric?.id}/edit`)}
						>Edit</Button
					>
					<Button variant="danger" onclick={() => (deleteOpen = true)}>Delete</Button>
				{/if}
				<Button onclick={() => (historyOpen = true)}>History</Button>
			</div>
		</div>

		<div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
			<div class="flex flex-col gap-3">
				<div class="rounded-lg border border-border bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold">Definition</h2>
					<p class="mt-1 text-[12px]">
						{describeDefinition(definition, {
							catalog: metric.catalog_id,
							namespace: metric.namespace,
							table: metric.table
						})}
					</p>
					{#if metric.description}
						<p class="mt-2 text-[12px] text-slate-600 dark:text-slate-300">{metric.description}</p>
					{/if}
					<dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
						<dt class="text-slate-600 dark:text-slate-300">Table</dt>
						<dd class="font-mono">{metric.namespace.join('.')}.{metric.table}</dd>
						<dt class="text-slate-600 dark:text-slate-300">Owner</dt>
						<dd class="font-mono">{metric.owner_user_id ?? '—'}</dd>
						{#if metric.certified_by}
							<dt class="text-slate-600 dark:text-slate-300">Certified by</dt>
							<dd class="font-mono">{metric.certified_by}</dd>
						{/if}
					</dl>
				</div>
			</div>

			<div class="rounded-lg border border-border bg-bg-panel p-4">
				<div class="flex flex-wrap items-end gap-2">
					<div class="min-w-40 flex-1">
						<Select
							label="Binding"
							bind:value={bindingId}
							options={bindings.map((binding) => ({ value: binding.id, label: binding.label }))}
							onchange={loadSQL}
						/>
					</div>
					{#if (definition.time_grains ?? []).length > 0}
						<div class="w-32">
							<Select
								label="Grain"
								bind:value={grain}
								options={[
									{ value: '', label: m.metrics_no_grouping() },
									...(definition.time_grains ?? []).map((g) => ({ value: g, label: g }))
								]}
								onchange={loadSQL}
							/>
						</div>
					{/if}
				</div>
				{#if sqlLoading}
					<Skeleton class="mt-2 h-20 w-full" />
				{:else if sqlError}
					<div class="mt-2">
						<ErrorPanel error={{ code: 'render_error', message: sqlError }} />
					</div>
				{:else}
					<pre
						class="mt-2 overflow-x-auto rounded bg-bg-toolbar p-2 font-mono text-[11px]">{sql}</pre>
					<div class="mt-2 flex justify-end gap-2">
						<CopyButton value={sql} label="SQL" />
						<Button size="sm" onclick={openInEditor}>Open in editor</Button>
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>

<HistoryDrawer
	bind:open={historyOpen}
	kind="semantic_metric"
	entityId={metricId}
	{workspaceId}
	onrestored={load}
/>

<ConfirmDialog
	bind:open={deleteOpen}
	title={m.metrics_delete_metric()}
	resourceName={metric?.label ?? ''}
	confirmLabel="Delete metric"
	consequences={[
		'Queries and links that reference this metric stop resolving.',
		m.metrics_the_metric_can_be_restored_from_trash()
	]}
	onconfirm={remove}
/>

<ConfirmDialog
	bind:open={certifyOpen}
	title={m.metrics_remove_certification()}
	resourceName={metric?.label ?? ''}
	confirmLabel="Remove certification"
	danger={false}
	consequences={['The certified badge disappears; the metric remains available.']}
	onconfirm={() => setCertified(false)}
/>
