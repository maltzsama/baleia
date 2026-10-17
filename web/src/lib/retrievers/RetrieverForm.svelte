<script lang="ts">
	import { onMount } from 'svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Textarea from '$lib/ui/Textarea.svelte';
	import {
		createRetriever,
		updateRetriever,
		listDestinations,
		previewCron,
		validateRetriever,
		RetrieverRequestError,
		type CronPreview,
		type Retriever,
		type RetrieverDestination
	} from './client';

	let {
		retriever = null,
		ondone
	}: {
		retriever?: Retriever | null;
		ondone?: (saved: Retriever) => void;
	} = $props();

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const presets = [
		{ label: 'Every hour', expression: '0 * * * *' },
		{ label: 'Every 6 hours', expression: '0 */6 * * *' },
		{ label: 'Every day at 03:00', expression: '0 3 * * *' },
		{ label: 'Every Monday at 03:00', expression: '0 3 * * 1' }
	];

	let destinations = $state<RetrieverDestination[]>([]);
	let name = $state('');
	let bindingId = $state('');
	let namespace = $state('');
	let table = $state('');
	let definition = $state('SELECT 1');
	let cron = $state('0 3 * * *');
	let preview = $state<CronPreview | null>(null);
	let previewError = $state('');
	let validation = $state<'idle' | 'valid' | 'invalid'>('idle');
	let error = $state<RetrieverRequestError | null>(null);
	let loading = $state(true);
	let saving = $state(false);
	let testing = $state(false);

	onMount(async () => {
		await workspaceStore.load();
		if (!workspaceId) {
			loading = false;
			return;
		}
		try {
			destinations = await listDestinations(workspaceId);
		} catch (e) {
			error = e instanceof RetrieverRequestError ? e : null;
		} finally {
			loading = false;
		}
		if (retriever) {
			name = retriever.name;
			bindingId = retriever.destination?.binding_id ?? retriever.dest_binding_id ?? '';
			namespace = retriever.dest_namespace;
			table = retriever.dest_table;
			definition = retriever.definition_sql;
			cron = retriever.cron_schedule;
		} else if (destinations[0]) {
			bindingId = destinations[0].binding_id;
		}
	});

	$effect(() => {
		if (!workspaceId) return;
		const expression = cron.trim();
		if (!expression) {
			preview = null;
			return;
		}
		let cancelled = false;
		const timer = setTimeout(async () => {
			try {
				const result = await previewCron(expression);
				if (!cancelled) {
					preview = result;
					previewError = '';
				}
			} catch (e) {
				if (!cancelled) {
					preview = null;
					previewError = e instanceof Error ? e.message : String(e);
				}
			}
		}, 400);
		return () => {
			cancelled = true;
			clearTimeout(timer);
		};
	});

	async function validate() {
		if (!workspaceId || !bindingId) return;
		testing = true;
		error = null;
		try {
			await validateRetriever(workspaceId, bindingId, definition);
			validation = 'valid';
		} catch (e) {
			validation = 'invalid';
			error = e instanceof RetrieverRequestError ? e : null;
		} finally {
			testing = false;
		}
	}

	async function save() {
		if (!workspaceId || !bindingId || !name.trim() || !namespace || !table) return;
		saving = true;
		error = null;
		try {
			const body = {
				name: name.trim(),
				definition_sql: definition,
				dest_binding_id: bindingId,
				dest_namespace: namespace.trim(),
				dest_table: table.trim(),
				cron_schedule: cron.trim(),
				refresh_type: 'full' as const
			};
			const saved = retriever
				? await updateRetriever(workspaceId, retriever.id, body)
				: await createRetriever(workspaceId, body);
			ondone?.(saved);
			if (!ondone) window.location.href = `/retrievers/${saved.id}`;
		} catch (e) {
			error = e instanceof RetrieverRequestError ? e : null;
		} finally {
			saving = false;
		}
	}

	function formatNext(value: string): string {
		return new Date(value).toLocaleString();
	}
</script>

<div class="flex flex-col gap-4">
	{#if loading}
		<p class="text-[12px] text-slate-600 dark:text-slate-300">Loading destinations…</p>
	{:else if destinations.length === 0}
		<div class="rounded-lg border border-border bg-bg-panel p-4 text-[13px]">
			<p class="font-semibold">No writable destination</p>
			<p class="mt-1 text-slate-600 dark:text-slate-300">
				A retriever needs an Iceberg catalog bound to a federated engine that this workspace may
				write to. Ask an owner to grant <span class="font-mono">write</span> on a catalog.
			</p>
		</div>
	{:else}
		<div class="grid gap-3 rounded-lg border border-border bg-bg-panel p-4 sm:grid-cols-2">
			<Input label="Name *" bind:value={name} placeholder="daily-orders" />
			<Select
				label="Destination *"
				bind:value={bindingId}
				options={destinations.map((destination) => ({
					value: destination.binding_id,
					label: `${destination.engine.name} · ${destination.catalog.name} (${destination.engine_catalog_name})`
				}))}
			/>
			<Input label="Namespace *" bind:value={namespace} placeholder="sales" class="font-mono" />
			<Input label="Table *" bind:value={table} placeholder="daily_orders" class="font-mono" />
		</div>

		<div class="rounded-lg border border-border bg-bg-panel p-4">
			<Textarea
				label="Definition (one SELECT) *"
				bind:value={definition}
				class="min-h-[160px] font-mono"
			/>
			<div class="mt-2 flex flex-wrap items-center gap-2">
				<Button size="sm" loading={testing} disabled={testing} onclick={validate}>
					Validate on the destination
				</Button>
				{#if validation === 'valid'}
					<span class="text-[11px] font-semibold text-success">Valid on the destination</span>
				{:else if validation === 'invalid'}
					<span class="text-[11px] font-semibold text-danger">Validation failed</span>
				{/if}
			</div>
		</div>

		<div class="rounded-lg border border-border bg-bg-panel p-4">
			<p class="text-[13px] font-semibold">Schedule</p>
			<div class="mt-2 flex flex-wrap gap-1.5">
				{#each presets as preset (preset.expression)}
					<Button size="sm" onclick={() => (cron = preset.expression)}>{preset.label}</Button>
				{/each}
			</div>
			<div class="mt-3">
				<Input label="Cron expression" bind:value={cron} class="font-mono" />
			</div>
			{#if previewError}
				<p class="mt-2 text-[11px] text-danger" role="alert">{previewError}</p>
			{:else if preview}
				{#if !preview.valid}
					<p class="mt-2 text-[11px] text-danger" role="alert">
						{preview.error ?? 'Invalid schedule'}
					</p>
				{:else}
					{#if !preview.min_interval_ok}
						<p class="mt-2 text-[11px] text-danger" role="alert">
							The minimum interval is 5 minutes.
						</p>
					{/if}
					<ul class="mt-2 grid gap-0.5 text-[11px] text-slate-600 dark:text-slate-300">
						{#each preview.next as next (next)}
							<li>{formatNext(next)}</li>
						{/each}
					</ul>
				{/if}
			{/if}
		</div>

		{#if error}
			<ErrorPanel {error} />
		{/if}
		<div class="flex justify-end gap-2">
			<Button variant="primary" loading={saving} disabled={saving} onclick={save}>
				{retriever ? 'Save changes' : 'Create retriever'}
			</Button>
		</div>
	{/if}
</div>
