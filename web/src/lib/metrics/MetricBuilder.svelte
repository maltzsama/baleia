<script lang="ts">
	import { onMount } from 'svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { listCatalogBindings } from '$lib/connections/api';
	import {
		aggregationsFor,
		createMetric,
		getTable,
		isTimeColumn,
		renderMetric,
		searchTables,
		updateMetric,
		validateMetric,
		type MetricDefinition,
		type MetadataColumn,
		type SearchResult,
		type SemanticMetric,
		type TableDetail
	} from '$lib/metrics/client';
	import { describeDefinition, slugify } from '$lib/metrics/definition';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Textarea from '$lib/ui/Textarea.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';

	let { metric = null }: { metric?: SemanticMetric | null } = $props();

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const spaces = $derived(savedQueries.spaces);

	let label = $state('');
	let slug = $state('');
	let slugEdited = $state(false);
	let description = $state('');
	let spaceId = $state('');

	let tableQuery = $state('');
	let results = $state<SearchResult[]>([]);
	let searching = $state(false);
	let table = $state<TableDetail | null>(null);
	let tableLoading = $state(false);

	let aggregation = $state('sum');
	let column = $state('');
	let filters = $state<{ column: string; op: string; value: string }[]>([]);
	let timeColumn = $state('');
	let grains = $state<string[]>(['month']);
	let customSql = $state('');
	let useCustom = $state(false);

	let bindings = $state<{ id: string; label: string }[]>([]);
	let bindingId = $state('');
	let preview = $state('');
	let previewError = $state('');
	let rendering = $state(false);
	let validating = $state(false);
	let validateOk = $state('');
	let validateError = $state('');
	let saving = $state(false);
	let saveError = $state('');

	const columns = $derived<MetadataColumn[]>(table?.columns ?? []);
	const measureColumns = $derived(columns.filter((c) => aggregationsFor(c).includes(aggregation)));
	const timeColumns = $derived(columns.filter(isTimeColumn));
	const definition = $derived<MetricDefinition>({
		type: 'simple',
		aggregation: useCustom ? undefined : aggregation,
		column: useCustom || aggregation === 'count' ? column || null : column || null,
		filters: filters
			.filter((f) => f.column && f.op)
			.map((f) => ({
				column: f.column,
				op: f.op,
				values:
					f.op === 'is_null' || f.op === 'is_not_null'
						? []
						: f.op === 'in' || f.op === 'not_in'
							? f.value
									.split(',')
									.map((v) => v.trim())
									.filter(Boolean)
							: [f.value]
			})),
		time_column: timeColumn || null,
		time_grains: timeColumn ? grains : [],
		custom_sql: useCustom && customSql.trim() ? customSql.trim() : null
	});

	let renderTimer: ReturnType<typeof setTimeout> | undefined;

	$effect(() => {
		// Re-render on any definition or binding change, debounced (#115).
		void definition;
		void bindingId;
		if (!workspaceId || !table) return;
		clearTimeout(renderTimer);
		renderTimer = setTimeout(() => void doRender(), 400);
		return () => clearTimeout(renderTimer);
	});

	$effect(() => {
		if (!slugEdited) slug = slugify(label);
	});

	async function init() {
		await workspaceStore.load();
		if (!workspaceId) return;
		await savedQueries.reload(workspaceId);
		if (metric) {
			label = metric.label;
			slug = metric.slug;
			slugEdited = true;
			description = metric.description ?? '';
			spaceId = metric.space_id ?? savedQueries.spaces[0]?.id ?? '';
			const d = metric.definition as unknown as MetricDefinition;
			aggregation = d.aggregation ?? 'count';
			column = d.column ?? '';
			timeColumn = d.time_column ?? '';
			grains = d.time_grains ?? ['month'];
			useCustom = !!d.custom_sql;
			customSql = d.custom_sql ?? '';
			filters = (d.filters ?? []).map((f) => ({
				column: f.column,
				op: f.op,
				value: (f.values ?? []).map(String).join(', ')
			}));
			bindingId = metric.binding_id ?? '';
			if (bindingId) await loadBindings(metric.catalog_id);
			results = await searchTables(workspaceId, metric.table);
			const match = results.find(
				(r) =>
					r.catalog_id === metric.catalog_id &&
					r.table === metric.table &&
					r.namespace.join('.') === metric.namespace.join('.')
			);
			if (match) await selectTable(match.table_id);
		} else {
			spaceId = savedQueries.spaces[0]?.id ?? '';
		}
	}

	async function loadBindings(catalogId: string) {
		const rows = await listCatalogBindings(catalogId);
		bindings = rows
			.filter((b) => b.enabled)
			.map((b) => ({ id: b.id, label: `${b.engine_catalog_name} (${b.connector ?? 'default'})` }));
		if (!bindingId) bindingId = bindings[0]?.id ?? '';
	}

	async function search() {
		if (!workspaceId || tableQuery.trim().length < 2) {
			results = [];
			return;
		}
		searching = true;
		try {
			results = await searchTables(workspaceId, tableQuery.trim());
		} finally {
			searching = false;
		}
	}

	async function selectTable(tableId: string) {
		if (!workspaceId) return;
		tableLoading = true;
		try {
			table = await getTable(workspaceId, tableId);
			results = [];
			tableQuery = `${table.namespace.join('.')}.${table.name}`;
			await loadBindings(table.catalog_id);
			if (!measureColumns.some((c) => c.name === column)) column = measureColumns[0]?.name ?? '';
		} finally {
			tableLoading = false;
		}
	}

	async function doRender() {
		if (!workspaceId || !table) return;
		rendering = true;
		previewError = '';
		try {
			preview = await renderMetric(workspaceId, {
				catalog_id: table.catalog_id,
				namespace: table.namespace,
				table: table.name,
				definition,
				binding_id: bindingId || null
			});
		} catch (e) {
			preview = '';
			previewError = e instanceof Error ? e.message : String(e);
		} finally {
			rendering = false;
		}
	}

	async function doValidate() {
		if (!workspaceId || !table) return;
		validating = true;
		validateOk = '';
		validateError = '';
		try {
			await validateMetric(workspaceId, {
				catalog_id: table.catalog_id,
				namespace: table.namespace,
				table: table.name,
				definition,
				binding_id: bindingId || null
			});
			validateOk = 'Valid on this engine.';
		} catch (e) {
			validateError = e instanceof Error ? e.message : String(e);
		} finally {
			validating = false;
		}
	}

	async function save() {
		if (!workspaceId || !table) {
			saveError = 'Choose a table first.';
			return;
		}
		saving = true;
		saveError = '';
		try {
			if (metric) {
				const updated = await updateMetric(workspaceId, metric.id, {
					slug,
					label,
					description,
					catalog_id: table.catalog_id,
					namespace: table.namespace,
					table: table.name,
					binding_id: bindingId || null,
					definition: definition as unknown as Record<string, unknown>,
					expected_version: metric.version
				});
				window.location.href = `/metrics/${updated.id}`;
			} else {
				const created = await createMetric(workspaceId, {
					slug,
					label,
					description,
					catalog_id: table.catalog_id,
					namespace: table.namespace,
					table: table.name,
					binding_id: bindingId || null,
					space_id: spaceId || undefined,
					definition: definition as unknown as Record<string, unknown>
				});
				window.location.href = `/metrics/${created.id}`;
			}
		} catch (e) {
			saveError = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}

	function addFilter() {
		filters = [...filters, { column: '', op: '=', value: '' }];
	}

	function removeFilter(index: number) {
		filters = filters.filter((_, i) => i !== index);
	}

	function opOptions(col: string): { value: string; label: string }[] {
		const c = columns.find((item) => item.name === col);
		const logical = c?.logical_type ?? 'other';
		const base = [
			{ value: '=', label: '=' },
			{ value: '!=', label: '!=' },
			{ value: 'in', label: 'in' },
			{ value: 'not_in', label: 'not in' },
			{ value: 'is_null', label: 'is null' },
			{ value: 'is_not_null', label: 'is not null' }
		];
		if (['int64', 'decimal', 'float64', 'date', 'timestamp', 'timestamptz'].includes(logical)) {
			return [
				...base,
				{ value: '>', label: '>' },
				{ value: '>=', label: '>=' },
				{ value: '<', label: '<' },
				{ value: '<=', label: '<=' }
			];
		}
		return base;
	}

	onMount(init);
</script>

<div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
	<div class="flex flex-col gap-3 rounded-lg border border-border bg-bg-panel p-4 text-[12px]">
		<h2 class="text-[13px] font-semibold">Identity</h2>
		<Input label="Label" bind:value={label} />
		<Input label="Slug" bind:value={slug} class="font-mono" oninput={() => (slugEdited = true)} />
		<Input label="Description" bind:value={description} />
		<Select
			label="Space"
			bind:value={spaceId}
			options={spaces
				.filter((space) => space.allowed_actions?.includes('edit_content'))
				.map((space) => ({ value: space.id, label: space.name }))}
		/>

		<h2 class="mt-2 text-[13px] font-semibold">Table</h2>
		<Input label="Search a table" bind:value={tableQuery} placeholder="orders" oninput={search} />
		{#if searching}<p class="text-[11px] text-slate-600 dark:text-slate-300">Searching…</p>{/if}
		{#if results.length > 0}
			<ul class="flex max-h-40 flex-col gap-1 overflow-y-auto rounded border border-border p-1">
				{#each results as result (result.table_id)}
					<li>
						<button
							type="button"
							class="w-full rounded px-1 py-1 text-left text-[11px] hover:bg-bg-toolbar"
							onclick={() => selectTable(result.table_id)}
						>
							{result.namespace.join('.')}.{result.table}
						</button>
					</li>
				{/each}
			</ul>
		{/if}
		{#if table}
			<p class="font-mono text-[11px]">{table.namespace.join('.')}.{table.name}</p>
			{#if tableLoading}<p class="text-[11px]">Loading columns…</p>{/if}
		{/if}

		<h2 class="mt-2 text-[13px] font-semibold">Measure</h2>
		<div class="flex flex-wrap gap-2">
			<label class="flex items-center gap-1 text-[11px]">
				<input type="checkbox" bind:checked={useCustom} /> Custom SQL expression
			</label>
		</div>
		{#if useCustom}
			<Textarea label="Custom aggregate (dialect only)" bind:value={customSql} class="font-mono" />
			<p class="text-[11px] text-amber-700 dark:text-amber-300">
				Non-portable: renders only on engines with enforced read-only or asserted read-only
				credentials.
			</p>
		{:else}
			<Select
				label="Aggregation"
				bind:value={aggregation}
				options={[
					{ value: 'sum', label: 'Sum' },
					{ value: 'count', label: 'Count' },
					{ value: 'count_distinct', label: 'Distinct count' },
					{ value: 'avg', label: 'Average' },
					{ value: 'min', label: 'Minimum' },
					{ value: 'max', label: 'Maximum' }
				]}
			/>
			<Select
				label="Column"
				bind:value={column}
				options={[
					...(aggregation === 'count' ? [{ value: '', label: 'All rows' }] : []),
					...measureColumns.map((c) => ({
						value: c.name,
						label: `${c.name} (${c.logical_type ?? c.native_type})`
					}))
				]}
			/>
		{/if}

		<h2 class="mt-2 text-[13px] font-semibold">Filters</h2>
		{#each filters as filter, index (index)}
			<div class="flex flex-wrap items-end gap-1">
				<Select
					label="Column"
					bind:value={filter.column}
					options={columns.map((c) => ({ value: c.name, label: c.name }))}
				/>
				<Select label="Operator" bind:value={filter.op} options={opOptions(filter.column)} />
				{#if filter.op !== 'is_null' && filter.op !== 'is_not_null'}
					<Input label="Value(s)" bind:value={filter.value} placeholder="paid, shipped" />
				{/if}
				<Button size="sm" variant="danger" onclick={() => removeFilter(index)}>Remove</Button>
			</div>
		{/each}
		<div>
			<Button size="sm" onclick={addFilter}>Add filter</Button>
		</div>

		<h2 class="mt-2 text-[13px] font-semibold">Time</h2>
		<Select
			label="Time column"
			bind:value={timeColumn}
			options={[
				{ value: '', label: 'No time grouping' },
				...timeColumns.map((c) => ({ value: c.name, label: c.name }))
			]}
		/>
		{#if timeColumn}
			<div class="flex flex-wrap gap-2 text-[11px]">
				{#each ['day', 'week', 'month', 'quarter', 'year'] as grain (grain)}
					<label class="flex items-center gap-1">
						<input
							type="checkbox"
							checked={grains.includes(grain)}
							onchange={(e) => {
								const checked = (e.target as HTMLInputElement).checked;
								grains = checked ? [...grains, grain] : grains.filter((g) => g !== grain);
							}}
						/>
						{grain}
					</label>
				{/each}
			</div>
		{/if}

		{#if bindings.length > 0}
			<Select
				label="Validation binding"
				bind:value={bindingId}
				options={bindings.map((binding) => ({ value: binding.id, label: binding.label }))}
			/>
		{/if}

		{#if saveError}
			<p class="text-danger" role="alert">{saveError}</p>
		{/if}
		{#if metric?.certified}
			<p class="text-[11px] text-amber-700 dark:text-amber-300">
				This metric is certified. Changing the definition without the certify action removes the
				certification.
			</p>
		{/if}
		<div class="flex justify-end gap-2">
			<Button loading={validating} disabled={validating || !table} onclick={doValidate}
				>Validate</Button
			>
			<Button variant="primary" loading={saving} disabled={saving || !table} onclick={save}
				>Save</Button
			>
		</div>
	</div>

	<div class="flex flex-col gap-2">
		<div class="rounded-lg border border-border bg-bg-panel p-4">
			<p class="text-[12px] font-semibold">Definition</p>
			<p class="mt-1 text-[11px] text-slate-600 dark:text-slate-300">
				{#if table}
					{describeDefinition(definition, {
						catalog: table.catalog_id,
						namespace: table.namespace,
						table: table.name
					})}
				{:else}
					Choose a table.
				{/if}
			</p>
		</div>
		<div class="rounded-lg border border-border bg-bg-panel p-4">
			<div class="flex items-center justify-between">
				<p class="text-[12px] font-semibold">SQL preview</p>
				{#if rendering}<span class="text-[10px] text-slate-600 dark:text-slate-300">rendering…</span
					>{/if}
			</div>
			{#if previewError}
				<div class="mt-2">
					<ErrorPanel error={{ code: 'invalid_definition', message: previewError }} />
				</div>
			{:else}
				<pre
					class="mt-2 overflow-x-auto rounded bg-bg-toolbar p-2 font-mono text-[11px]">{preview ||
						'—'}</pre>
			{/if}
			{#if validateOk}<Badge tone="success">{validateOk}</Badge>{/if}
			{#if validateError}
				<div class="mt-2">
					<ErrorPanel error={{ code: 'engine_error', message: validateError }} />
				</div>
			{/if}
		</div>
	</div>
</div>
