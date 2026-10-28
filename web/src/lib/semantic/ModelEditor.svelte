<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onDestroy, onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { listCatalogs, type Catalog } from '$lib/catalogs/admin';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import { getTable, searchTables, type SearchResult } from '$lib/metrics/client';
	import {
		AGGREGATIONS,
		bindingOptions,
		parseFilterValues,
		GRAINS,
		SemanticRequestError,
		compileExplore,
		createModel,
		defaultGrains,
		definitionOf,
		dimensionsFromColumns,
		isTimeType,
		listModels,
		slugify,
		updateModel,
		type BindingOption,
		type FilterOp,
		type LogicalType,
		type ModelDefinition,
		type ModelDimension,
		type ModelJoin,
		type ModelMetric,
		type Relationship,
		type SemanticModel
	} from './explore';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Textarea from '$lib/ui/Textarea.svelte';
	import SqlSourceEditor from './SqlSourceEditor.svelte';
	import CodeBlock from '$lib/ui/patterns/CodeBlock.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	let {
		model = null,
		onsaved
	}: { model?: SemanticModel | null; onsaved?: (model: SemanticModel) => void } = $props();

	const TYPES: LogicalType[] = [
		'string',
		'int64',
		'float64',
		'boolean',
		'date',
		'timestamp',
		'timestamptz'
	];
	const RELATIONSHIPS: Relationship[] = ['many_to_one', 'one_to_one', 'one_to_many'];
	const FILTER_OPS: FilterOp[] = [
		'=',
		'!=',
		'>',
		'>=',
		'<',
		'<=',
		'in',
		'not_in',
		'is_null',
		'is_not_null'
	];

	// Editing state: the definition below strips these helper fields.
	// `decimals` is typed as the text input's value; a number input may still
	// hand back a number or null, which formatOf accepts.
	type EditDimension = ModelDimension & { decimals: string };
	type EditMetric = ModelMetric & {
		mode: 'aggregate' | 'sql' | 'derived';
		filterText: string[];
		decimals: string;
	};

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const spaces = $derived(savedQueries.spaces);
	const canCertify = $derived(!model || !!model.allowed_actions?.includes('certify'));

	let label = $state('');
	let slug = $state('');
	let slugEdited = $state(false);
	let description = $state('');
	let spaceId = $state('');
	let certified = $state(false);
	let catalogId = $state('');
	let bindingId = $state('');
	let catalogs = $state<Catalog[]>([]);
	let bindings = $state<BindingOption[]>([]);

	let sourceKind = $state<'table' | 'sql'>('table');
	let namespace = $state<string[]>([]);
	let table = $state('');
	let sourceSql = $state('');
	let tableQuery = $state('');
	let tableResults = $state<SearchResult[]>([]);
	let primaryKey = $state<string[]>([]);

	let dimensions = $state<EditDimension[]>([]);
	let metrics = $state<EditMetric[]>([]);
	let joins = $state<ModelJoin[]>([]);
	let others = $state<SemanticModel[]>([]);

	let saving = $state(false);
	let saveError = $state<SemanticRequestError | null>(null);
	let previewSql = $state('');
	let previewNotes = $state<string[]>([]);
	let previewError = $state('');
	let previewing = $state(false);

	const dimensionNames = $derived(dimensions.map((d) => d.name).filter(Boolean));
	const metricNames = $derived(metrics.map((mt) => mt.name).filter(Boolean));
	const otherOptions = $derived(
		others.filter((o) => o.slug !== slug).map((o) => ({ value: o.slug, label: o.label }))
	);
	const definition = $derived<ModelDefinition>({
		label: label.trim() || undefined,
		description: description.trim() || undefined,
		source:
			sourceKind === 'table'
				? { kind: 'table', namespace, table }
				: { kind: 'sql', sql: sourceSql },
		primary_key: primaryKey,
		dimensions: dimensions.map(({ decimals, ...d }) => ({
			...d,
			label: d.label || undefined,
			description: d.description || undefined,
			sql: d.sql || undefined,
			hidden: d.hidden || undefined,
			grains: isTimeType(d.type) && d.grains?.length ? d.grains : undefined,
			format: formatOf(decimals, isNumericType(d.type))
		})),
		metrics: metrics.map(toMetric),
		joins: joins.length ? joins : undefined
	});

	$effect(() => {
		if (!slugEdited && !model) slug = slugify(label);
	});

	function load(existing: SemanticModel) {
		const def = definitionOf(existing);
		label = existing.label;
		slug = existing.slug;
		slugEdited = true;
		description = existing.description ?? '';
		spaceId = existing.space_id ?? '';
		certified = existing.certified;
		catalogId = existing.catalog_id;
		bindingId = existing.binding_id ?? '';
		sourceKind = def.source?.kind ?? 'table';
		namespace = def.source?.namespace ?? [];
		table = def.source?.table ?? '';
		sourceSql = def.source?.sql ?? '';
		primaryKey = def.primary_key ?? [];
		dimensions = (def.dimensions ?? []).map(withDefaults);
		metrics = (def.metrics ?? []).map(toEditMetric);
		joins = (def.joins ?? []).map((j) => ({ ...j, on: j.on.map((k) => ({ ...k })) }));
	}

	async function loadBindings(id: string) {
		if (!id) {
			bindings = [];
			return;
		}
		bindings = await bindingOptions(id);
		if (!bindingId || !bindings.some((b) => b.id === bindingId)) {
			bindingId = bindings[0]?.id ?? '';
		}
	}

	async function searchSource() {
		if (!workspaceId || tableQuery.trim().length < 2) {
			tableResults = [];
			return;
		}
		tableResults = await searchTables(workspaceId, tableQuery.trim());
	}

	async function pickTable(result: SearchResult) {
		const detail = await getTable(workspaceId, result.table_id);
		catalogId = detail.catalog_id;
		namespace = detail.namespace;
		table = detail.name;
		tableQuery = `${detail.namespace.join('.')}.${detail.name}`;
		tableResults = [];
		await loadBindings(catalogId);
		if (!label) label = detail.name;
		if (dimensions.length === 0) {
			dimensions = dimensionsFromColumns(detail.columns ?? []).map(withDefaults);
			const key = (detail.columns ?? []).find((c) => /^id$/i.test(c.name));
			if (key && primaryKey.length === 0) primaryKey = [key.name];
		}
	}

	function regenerate() {
		if (!workspaceId || !table) return;
		const match = tableResults.find((r) => r.table === table);
		void (async () => {
			const results = match ? [match] : await searchTables(workspaceId, table);
			const found = results.find(
				(r) => r.table === table && r.namespace.join('.') === namespace.join('.')
			);
			if (!found) return;
			const detail = await getTable(workspaceId, found.table_id);
			const existing = new Set(dimensions.map((d) => d.name));
			dimensions = [
				...dimensions,
				...dimensionsFromColumns(detail.columns ?? [])
					.filter((d) => !existing.has(d.name))
					.map(withDefaults)
			];
		})();
	}

	// Inputs bind to strings, so optional text fields start empty rather than undefined.
	function withDefaults(d: ModelDimension): EditDimension {
		return {
			...d,
			label: d.label ?? '',
			description: d.description ?? '',
			sql: d.sql ?? '',
			hidden: !!d.hidden,
			decimals: d.format ? String(d.format.decimals) : ''
		};
	}
	function isNumericType(type: LogicalType | undefined): boolean {
		return type === 'int64' || type === 'float64';
	}
	// A number input binds a number (or null when cleared), not the typed text.
	function formatOf(decimals: string | number | null | undefined, allowed = true) {
		const text = String(decimals ?? '').trim();
		const n = Number(text);
		return allowed && text !== '' && Number.isInteger(n) && n >= 0
			? { type: 'number', decimals: n }
			: undefined;
	}
	function toEditMetric(mt: ModelMetric): EditMetric {
		return {
			...mt,
			label: mt.label ?? '',
			dimension: mt.dimension ?? '',
			sql: mt.sql ?? '',
			filters: (mt.filters ?? []).map((f) => ({ ...f })),
			filterText: (mt.filters ?? []).map((f) => (f.values ?? []).join(', ')),
			mode: mt.derived ? 'derived' : mt.sql ? 'sql' : 'aggregate',
			decimals: mt.format ? String(mt.format.decimals) : ''
		};
	}
	function toMetric({ mode, filterText, decimals, ...mt }: EditMetric): ModelMetric {
		const base = {
			name: mt.name,
			label: mt.label || undefined,
			format: formatOf(decimals),
			// Kept so the migrated v1 metric's page still finds this metric.
			migrated_from: mt.migrated_from || undefined
		};
		if (mode === 'derived') return { ...base, derived: mt.derived };
		const filters = (mt.filters ?? [])
			.map((f, i) => {
				const type = dimensions.find((d) => d.name === f.column)?.type;
				const field = {
					name: f.column,
					label: f.column,
					kind: 'dimension' as const,
					type,
					grains: [],
					model: ''
				};
				return {
					column: f.column,
					op: f.op,
					values: parseFilterValues(field, f.op as FilterOp, filterText[i] ?? '')
				};
			})
			.filter((f) => f.column);
		const scoped = { ...base, filters: filters.length ? filters : undefined };
		if (mode === 'sql') return { ...scoped, sql: mt.sql };
		return { ...scoped, aggregation: mt.aggregation, dimension: mt.dimension || undefined };
	}
	function addDimension() {
		dimensions = [...dimensions, withDefaults({ name: '', type: 'string' })];
	}
	function removeDimension(index: number) {
		const removed = dimensions[index].name;
		dimensions = dimensions.filter((_, i) => i !== index);
		primaryKey = primaryKey.filter((name) => name !== removed);
	}
	function setType(index: number, type: LogicalType) {
		const d = dimensions[index];
		dimensions[index] = {
			...d,
			type,
			grains: isTimeType(type) ? (d.grains?.length ? d.grains : defaultGrains(type)) : undefined
		};
	}
	function toggleGrain(index: number, grain: string) {
		const d = dimensions[index];
		const grains = d.grains ?? [];
		dimensions[index] = {
			...d,
			grains: grains.includes(grain) ? grains.filter((g) => g !== grain) : [...grains, grain]
		};
	}
	function togglePrimaryKey(name: string) {
		primaryKey = primaryKey.includes(name)
			? primaryKey.filter((n) => n !== name)
			: [...primaryKey, name];
	}

	function addMetric(derived = false) {
		metrics = [
			...metrics,
			toEditMetric(
				derived
					? {
							name: '',
							derived: { left: metricNames[0] ?? '', op: '/', right: metricNames[1] ?? '' }
						}
					: { name: '', aggregation: 'count' }
			)
		];
	}
	function addMetricFilter(index: number) {
		const mt = metrics[index];
		mt.filters = [...(mt.filters ?? []), { column: dimensionNames[0] ?? '', op: '=' }];
		mt.filterText = [...mt.filterText, ''];
	}
	function removeMetricFilter(index: number, filter: number) {
		const mt = metrics[index];
		mt.filters = (mt.filters ?? []).filter((_, i) => i !== filter);
		mt.filterText = mt.filterText.filter((_, i) => i !== filter);
	}
	function removeMetric(index: number) {
		metrics = metrics.filter((_, i) => i !== index);
	}
	function setAggregation(index: number, aggregation: string) {
		const mt = metrics[index];
		metrics[index] = {
			...mt,
			aggregation: aggregation as ModelMetric['aggregation'],
			dimension: aggregation === 'count' ? mt.dimension : mt.dimension || (dimensionNames[0] ?? '')
		};
	}

	function addJoin() {
		const target = otherOptions[0]?.value;
		if (!target) return;
		joins = [...joins, { to: target, relationship: 'many_to_one', on: [{ from: '', to: '' }] }];
	}
	function removeJoin(index: number) {
		joins = joins.filter((_, i) => i !== index);
	}
	function targetDimensions(slugName: string): string[] {
		const target = others.find((o) => o.slug === slugName);
		return (definitionOf(target ?? ({ definition: {} } as SemanticModel)).dimensions ?? []).map(
			(d) => d.name
		);
	}
	function addKey(index: number) {
		joins[index].on = [...joins[index].on, { from: '', to: '' }];
	}
	function removeKey(index: number, key: number) {
		joins[index].on = joins[index].on.filter((_, i) => i !== key);
	}

	function body() {
		return {
			label: label.trim(),
			description: description.trim(),
			catalog_id: catalogId,
			binding_id: bindingId || null,
			// Only a space manager sends certification: omitting it lets the API
			// drop the badge when an editor changes a certified definition.
			...(canCertify ? { certified } : {}),
			definition: definition as unknown as Record<string, unknown>
		};
	}

	async function save() {
		if (!workspaceId) return;
		saving = true;
		saveError = null;
		try {
			const saved = model
				? await updateModel(workspaceId, model.id, {
						...body(),
						slug,
						expected_version: model.version
					})
				: await createModel(workspaceId, { ...body(), slug, space_id: spaceId || null });
			toasts.success(m.models_saved());
			if (onsaved) onsaved(saved);
			else void goto(resolve(`/models/${saved.id}`));
		} catch (e) {
			saveError =
				e instanceof SemanticRequestError
					? e
					: new SemanticRequestError(e instanceof Error ? e.message : String(e));
		} finally {
			saving = false;
		}
	}

	async function preview() {
		if (!model || !workspaceId) return;
		previewing = true;
		previewError = '';
		previewSql = '';
		try {
			const out = await compileExplore(
				workspaceId,
				{
					model: model.slug,
					dimensions: dimensionNames.slice(0, 2),
					metrics: metricNames.slice(0, 1)
				},
				bindingId || null
			);
			previewSql = out.sql;
			previewNotes = out.notes ?? [];
		} catch (e) {
			previewError = e instanceof Error ? e.message : String(e);
		} finally {
			previewing = false;
		}
	}

	let alive = true;
	onDestroy(() => (alive = false));

	// A failed lookup is shown next to its control with a retry (#460); an
	// empty list only means there is nothing to pick. Retrying keeps edits.
	let catalogsError = $state('');
	let othersError = $state('');

	async function loadCatalogOptions() {
		catalogsError = '';
		try {
			const rows = await listCatalogs({ limit: 200 });
			if (alive) catalogs = rows;
		} catch (e) {
			if (alive) catalogsError = e instanceof Error ? e.message : String(e);
		}
	}

	async function loadJoinOptions() {
		othersError = '';
		try {
			const rows = await listModels(workspaceId);
			if (alive) others = rows;
		} catch (e) {
			if (alive) othersError = e instanceof Error ? e.message : String(e);
		}
	}

	onMount(async () => {
		await workspaceStore.load();
		if (!alive || !workspaceId) return;
		await savedQueries.reload(workspaceId);
		if (!alive) return;
		await Promise.all([loadCatalogOptions(), loadJoinOptions()]);
		if (!alive) return;
		if (model) {
			load(model);
			await loadBindings(catalogId);
		} else {
			spaceId =
				savedQueries.spaces.find((s) => s.my_role === 'editor' || s.my_role === 'manager')?.id ??
				'';
		}
	});
</script>

<div class="flex flex-col gap-4" data-testid="model-editor">
	<section class="rounded-lg border border-border bg-bg-panel p-4">
		<h2 class="mb-3 text-[13px] font-semibold">{m.models_details()}</h2>
		<div class="grid gap-3 sm:grid-cols-2">
			<Input label={m.models_label()} bind:value={label} required />
			<Input
				label={m.models_slug()}
				value={slug}
				class="font-mono"
				oninput={(e) => {
					slugEdited = true;
					slug = (e.target as HTMLInputElement).value;
				}}
			/>
			<Textarea
				label={m.models_description()}
				bind:value={description}
				rows={2}
				class="sm:col-span-2"
			/>
			{#if !model}
				<Select
					label={m.common_space()}
					bind:value={spaceId}
					options={spaces
						.filter((s) => s.my_role === 'editor' || s.my_role === 'manager')
						.map((s) => ({ value: s.id, label: s.name }))}
				/>
			{/if}
			{#if canCertify}
				<div class="flex items-end">
					<Checkbox label={m.models_certified()} bind:checked={certified} />
				</div>
			{/if}
		</div>
	</section>

	<section class="rounded-lg border border-border bg-bg-panel p-4">
		<h2 class="mb-3 text-[13px] font-semibold">{m.models_source()}</h2>
		<div class="mb-3 max-w-xs">
			<Select
				label={m.models_source_kind()}
				bind:value={sourceKind}
				options={[
					{ value: 'table', label: m.models_source_table() },
					{ value: 'sql', label: m.models_source_sql() }
				]}
			/>
		</div>
		{#if sourceKind === 'table'}
			<div class="relative max-w-md">
				<Input
					label={m.models_pick_table()}
					bind:value={tableQuery}
					oninput={() => void searchSource()}
				/>
				{#if tableResults.length > 0}
					<ul
						class="absolute z-10 mt-1 max-h-56 w-full overflow-y-auto rounded-md border border-border bg-bg-panel shadow-lg"
					>
						{#each tableResults as result (result.table_id)}
							<li>
								<button
									type="button"
									class="w-full px-3 py-1.5 text-left text-[12px] hover:bg-bg-toolbar"
									onclick={() => pickTable(result)}
								>
									{result.namespace.join('.')}.{result.table}
								</button>
							</li>
						{/each}
					</ul>
				{/if}
			</div>
			{#if table}
				<p class="mt-2 font-mono text-[11px] text-text-muted">{namespace.join('.')}.{table}</p>
			{/if}
		{:else}
			<div class="max-w-md">
				<Select
					label={m.models_catalog()}
					value={catalogId}
					placeholder={m.models_pick_catalog()}
					options={catalogs.flatMap((c) =>
						c.id && c.name ? [{ value: c.id, label: c.name }] : []
					)}
					onchange={(e) => {
						catalogId = (e.target as HTMLSelectElement).value;
						void loadBindings(catalogId);
					}}
				/>
				{#if catalogsError}
					<div class="mt-2">
						<ErrorState
							title={m.models_could_not_load_catalogs()}
							message={catalogsError}
							onretry={loadCatalogOptions}
						/>
					</div>
				{/if}
			</div>
			<div class="mt-3">
				<SqlSourceEditor
					label={m.models_sql()}
					bind:value={sourceSql}
					dialect={bindings.find((b) => b.id === bindingId)?.codemirrorBase}
				/>
			</div>
		{/if}
		<div class="mt-3 max-w-md">
			<Select
				label={m.models_binding()}
				bind:value={bindingId}
				options={bindings.map((b) => ({ value: b.id, label: b.label }))}
			/>
		</div>
		<fieldset class="mt-3">
			<legend class="text-[11px] font-semibold text-text-muted">{m.models_primary_key()}</legend>
			<div class="mt-1 flex flex-wrap gap-3">
				{#each dimensionNames as name (name)}
					<Checkbox
						label={name}
						checked={primaryKey.includes(name)}
						onchange={() => togglePrimaryKey(name)}
					/>
				{:else}
					<span class="text-[11px] text-text-muted">{m.models_primary_key_hint()}</span>
				{/each}
			</div>
		</fieldset>
	</section>

	<section class="rounded-lg border border-border bg-bg-panel p-4">
		<div class="mb-3 flex items-center justify-between">
			<h2 class="text-[13px] font-semibold">{m.models_dimensions()}</h2>
			<div class="flex gap-2">
				{#if sourceKind === 'table' && table}
					<Button size="sm" onclick={regenerate}>{m.models_add_missing_columns()}</Button>
				{/if}
				<Button size="sm" onclick={addDimension}>{m.models_add_dimension()}</Button>
			</div>
		</div>
		{#each dimensions as dimension, index (index)}
			<div class="mb-3 rounded border border-border/60 p-2">
				<div class="grid gap-2 sm:grid-cols-[1fr_1fr_8rem_auto] sm:items-end">
					<Input label={m.models_name()} bind:value={dimension.name} class="font-mono" />
					<Input label={m.models_label()} bind:value={dimension.label} />
					<Select
						label={m.models_type()}
						value={dimension.type}
						options={TYPES.map((t) => ({ value: t, label: t }))}
						onchange={(e) => setType(index, (e.target as HTMLSelectElement).value as LogicalType)}
					/>
					<Button size="sm" onclick={() => removeDimension(index)}>{m.common_remove()}</Button>
				</div>
				<div class="mt-2 grid gap-2 sm:grid-cols-[1fr_auto] sm:items-end">
					<Input label={m.models_sql_expression()} bind:value={dimension.sql} class="font-mono" />
					<Checkbox label={m.models_hidden()} bind:checked={dimension.hidden} />
				</div>
				<div class="mt-2 grid gap-2 sm:grid-cols-[1fr_8rem] sm:items-end">
					<Input label={m.models_description()} bind:value={dimension.description} />
					{#if isNumericType(dimension.type)}
						<Input
							label={m.models_decimals()}
							type="number"
							bind:value={dimension.decimals}
							placeholder={m.models_decimals_none()}
						/>
					{/if}
				</div>
				{#if isTimeType(dimension.type)}
					<fieldset class="mt-2">
						<legend class="text-[11px] font-semibold text-text-muted">{m.models_grains()}</legend>
						<div class="mt-1 flex flex-wrap gap-3">
							{#each GRAINS as grain (grain)}
								<Checkbox
									label={grain}
									checked={dimension.grains?.includes(grain) ?? false}
									onchange={() => toggleGrain(index, grain)}
								/>
							{/each}
						</div>
					</fieldset>
				{/if}
			</div>
		{:else}
			<p class="text-[12px] text-text-muted">{m.models_no_dimensions()}</p>
		{/each}
	</section>

	<section class="rounded-lg border border-border bg-bg-panel p-4">
		<div class="mb-3 flex items-center justify-between">
			<h2 class="text-[13px] font-semibold">{m.models_metrics()}</h2>
			<div class="flex gap-2">
				<Button size="sm" onclick={() => addMetric(false)}>{m.models_add_metric()}</Button>
				<Button size="sm" onclick={() => addMetric(true)} disabled={metricNames.length < 2}>
					{m.models_add_derived()}
				</Button>
			</div>
		</div>
		{#each metrics as metric, index (index)}
			<div class="mb-3 rounded border border-border/60 p-2">
				<div class="grid gap-2 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
					<Input label={m.models_name()} bind:value={metric.name} class="font-mono" />
					<Input label={m.models_label()} bind:value={metric.label} />
					<Button size="sm" onclick={() => removeMetric(index)}>{m.common_remove()}</Button>
				</div>
				<div class="mt-2 grid gap-2 sm:grid-cols-[1fr_8rem]">
					<Select
						label={m.models_metric_kind()}
						bind:value={metric.mode}
						options={[
							{ value: 'aggregate', label: m.models_metric_kind_aggregate() },
							{ value: 'sql', label: m.models_metric_kind_sql() },
							{ value: 'derived', label: m.models_metric_kind_derived() }
						]}
						onchange={() => {
							if (metric.mode === 'derived' && !metric.derived)
								metric.derived = {
									left: metricNames[0] ?? '',
									op: '/',
									right: metricNames[1] ?? ''
								};
							if (metric.mode === 'aggregate' && !metric.aggregation) metric.aggregation = 'count';
						}}
					/>
					<Input
						label={m.models_decimals()}
						type="number"
						bind:value={metric.decimals}
						placeholder={m.models_decimals_none()}
					/>
				</div>
				{#if metric.mode === 'derived' && metric.derived}
					<div class="mt-2 grid grid-cols-3 gap-2">
						<Select
							label={m.models_derived_left()}
							bind:value={metric.derived.left}
							options={metricNames
								.filter((n) => n !== metric.name)
								.map((n) => ({ value: n, label: n }))}
						/>
						<Select
							label={m.models_derived_operator()}
							bind:value={metric.derived.op}
							options={['+', '-', '*', '/'].map((op) => ({ value: op, label: op }))}
						/>
						<Select
							label={m.models_derived_right()}
							bind:value={metric.derived.right}
							options={metricNames
								.filter((n) => n !== metric.name)
								.map((n) => ({ value: n, label: n }))}
						/>
					</div>
					{#if metric.derived.op === '/'}
						<p class="mt-1 text-[10px] text-text-muted">{m.models_safe_divide_hint()}</p>
					{/if}
				{:else}
					{#if metric.mode === 'sql'}
						<div class="mt-2">
							<Input
								label={m.models_metric_sql()}
								bind:value={metric.sql}
								class="font-mono"
								placeholder={m.semantic_modeleditor_sum_amount_sum_refund()}
							/>
							<p class="mt-1 text-[10px] text-text-muted">{m.models_metric_sql_hint()}</p>
						</div>
					{:else}
						<div class="mt-2 grid gap-2 sm:grid-cols-2">
							<Select
								label={m.models_aggregation()}
								value={metric.aggregation ?? 'count'}
								options={AGGREGATIONS.map((a) => ({ value: a, label: a }))}
								onchange={(e) => setAggregation(index, (e.target as HTMLSelectElement).value)}
							/>
							<Select
								label={m.models_dimension()}
								bind:value={metric.dimension}
								placeholder={metric.aggregation === 'count' ? m.models_all_rows() : undefined}
								options={[
									...(metric.aggregation === 'count'
										? [{ value: '', label: m.models_all_rows() }]
										: []),
									...dimensionNames.map((n) => ({ value: n, label: n }))
								]}
							/>
						</div>
					{/if}
					<fieldset class="mt-2">
						<legend class="text-[11px] font-semibold text-text-muted"
							>{m.models_metric_filters()}</legend
						>
						{#each metric.filters ?? [] as filter, filterIndex (filterIndex)}
							<div class="mt-1 grid grid-cols-[1fr_7rem_1fr_auto] items-end gap-2">
								<Select
									label={m.explore_filter_field()}
									bind:value={filter.column}
									options={dimensionNames.map((n) => ({ value: n, label: n }))}
								/>
								<Select
									label={m.explore_filter_operator()}
									bind:value={filter.op}
									options={FILTER_OPS.map((op) => ({ value: op, label: op }))}
								/>
								{#if filter.op === 'is_null' || filter.op === 'is_not_null'}
									<div></div>
								{:else}
									<Input
										label={m.explore_filter_value()}
										bind:value={metric.filterText[filterIndex]}
									/>
								{/if}
								<Button size="sm" onclick={() => removeMetricFilter(index, filterIndex)}>
									{m.common_remove()}
								</Button>
							</div>
						{/each}
						<div class="mt-1">
							<Button
								size="sm"
								onclick={() => addMetricFilter(index)}
								disabled={dimensionNames.length === 0}
							>
								{m.explore_add_filter()}
							</Button>
						</div>
					</fieldset>
				{/if}
			</div>
		{:else}
			<p class="text-[12px] text-text-muted">{m.models_no_metrics()}</p>
		{/each}
	</section>

	<section class="rounded-lg border border-border bg-bg-panel p-4">
		<div class="mb-3 flex items-center justify-between">
			<h2 class="text-[13px] font-semibold">{m.models_joins()}</h2>
			<Button size="sm" onclick={addJoin} disabled={otherOptions.length === 0}>
				{m.models_add_join()}
			</Button>
		</div>
		{#if othersError}
			<div class="mb-3">
				<ErrorState
					title={m.models_could_not_load_models_to_join()}
					message={othersError}
					onretry={loadJoinOptions}
				/>
			</div>
		{/if}
		{#if joins.length > 0}
			<div
				class="mb-3 rounded bg-bg-toolbar p-3 font-mono text-[11px]"
				data-testid="join-tree"
				aria-label={m.models_join_tree()}
			>
				<div class="font-semibold">{slug || m.models_this_model()}</div>
				{#each joins as join, index (index)}
					<div class="ml-4 border-l border-border pl-3">
						<span class="text-text-muted">└ {join.type ?? 'left'} · {join.relationship} →</span>
						{join.to}
					</div>
				{/each}
			</div>
		{/if}
		{#each joins as join, index (index)}
			<div class="mb-3 rounded border border-border/60 p-2">
				<div class="grid gap-2 sm:grid-cols-[1fr_1fr_8rem_auto] sm:items-end">
					<Select label={m.models_join_target()} bind:value={join.to} options={otherOptions} />
					<Select
						label={m.models_relationship()}
						bind:value={join.relationship}
						options={RELATIONSHIPS.map((r) => ({ value: r, label: r }))}
					/>
					<Select
						label={m.models_join_type()}
						value={join.type ?? 'left'}
						options={[
							{ value: 'left', label: 'left' },
							{ value: 'inner', label: 'inner' }
						]}
						onchange={(e) =>
							(join.type = (e.target as HTMLSelectElement).value as 'left' | 'inner')}
					/>
					<Button size="sm" onclick={() => removeJoin(index)}>{m.common_remove()}</Button>
				</div>
				{#each join.on as key, keyIndex (keyIndex)}
					<div class="mt-2 grid grid-cols-[1fr_1fr_auto] items-end gap-2">
						<Select
							label={m.models_key_from()}
							bind:value={key.from}
							placeholder={m.models_pick_column()}
							options={dimensionNames.map((n) => ({ value: n, label: n }))}
						/>
						<Select
							label={m.models_key_to()}
							bind:value={key.to}
							placeholder={m.models_pick_column()}
							options={targetDimensions(join.to).map((n) => ({ value: n, label: n }))}
						/>
						<Button
							size="sm"
							onclick={() => removeKey(index, keyIndex)}
							disabled={join.on.length === 1}
						>
							{m.common_remove()}
						</Button>
					</div>
				{/each}
				<div class="mt-2">
					<Button size="sm" onclick={() => addKey(index)}>{m.models_add_key()}</Button>
				</div>
			</div>
		{:else}
			<p class="text-[12px] text-text-muted">{m.models_no_joins()}</p>
		{/each}
	</section>

	<section class="rounded-lg border border-border bg-bg-panel p-4">
		<div class="mb-2 flex items-center justify-between">
			<h2 class="text-[13px] font-semibold">{m.models_validation_preview()}</h2>
			<Button size="sm" onclick={preview} disabled={!model || previewing} loading={previewing}>
				{m.models_preview()}
			</Button>
		</div>
		{#if !model}
			<p class="text-[12px] text-text-muted">{m.models_preview_after_save()}</p>
		{:else if previewError}
			<p class="text-[12px] text-danger" role="alert">{previewError}</p>
		{:else if previewSql}
			<CodeBlock
				code={previewSql}
				language="sql"
				label={m.explore_compiled_sql()}
				dialect={bindings.find((b) => b.id === bindingId)?.codemirrorBase}
			/>
			{#each previewNotes as note (note)}
				<Badge tone="info">{note}</Badge>
			{/each}
		{:else}
			<p class="text-[12px] text-text-muted">{m.models_preview_hint()}</p>
		{/if}
	</section>

	<!-- Save and Cancel stay at the bottom of the viewport on a long model
	     (#479), with the save error next to them. -->
	<div
		class="sticky bottom-0 z-10 -mx-1 flex flex-col gap-2 border-t border-border bg-bg-panel/95 px-1 py-3 backdrop-blur"
	>
		{#if saveError}
			<ErrorPanel error={{ code: saveError.code ?? 'validation_error' }} />
			<p class="font-mono text-[11px] text-danger" role="alert">{saveError.message}</p>
		{/if}
		<div class="flex justify-end gap-2">
			<Button onclick={() => goto(resolve('/models'))} disabled={saving}>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				onclick={save}
				loading={saving}
				disabled={saving || !label.trim() || !slug.trim() || !catalogId || primaryKey.length === 0}
			>
				{m.common_save()}
			</Button>
		</div>
	</div>
</div>
