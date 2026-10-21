<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { goto } from '$app/navigation';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { definitionOf, listModels, type SemanticModel } from '$lib/semantic/explore';
	import MigrateV1Dialog from '$lib/semantic/MigrateV1Dialog.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const spaces = $derived(savedQueries.spaces);

	let models = $state<SemanticModel[]>([]);
	let loading = $state(true);
	let error = $state('');
	let q = $state('');
	let spaceId = $state('');
	let migrateOpen = $state(false);

	const visible = $derived(
		models.filter((model) => {
			const needle = q.trim().toLowerCase();
			return (
				!needle ||
				model.label.toLowerCase().includes(needle) ||
				model.slug.toLowerCase().includes(needle)
			);
		})
	);

	async function load() {
		if (!workspaceId) return;
		loading = true;
		error = '';
		try {
			await savedQueries.reload(workspaceId);
			models = await listModels(workspaceId, spaceId || undefined);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	function source(model: SemanticModel): string {
		const def = definitionOf(model);
		if (def.source?.kind === 'sql') return m.models_source_sql();
		return [...(def.source?.namespace ?? []), def.source?.table ?? ''].filter(Boolean).join('.');
	}

	onMount(async () => {
		await workspaceStore.load();
		await load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.models_title()}</h1>
			<p class="mt-1 text-[13px] text-text-muted">{m.models_subtitle()}</p>
		</div>
		<div class="flex gap-2">
			<Button onclick={() => (migrateOpen = true)}>{m.models_migrate_button()}</Button>
			<Button onclick={() => goto(resolve('/explore'))}>{m.models_open_explore()}</Button>
			<Button variant="primary" onclick={() => goto(resolve('/models/new'))}
				>{m.models_new()}</Button
			>
		</div>
	</div>

	<div class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-3">
		<Input label={m.common_search()} bind:value={q} />
		<Select
			label={m.common_space()}
			bind:value={spaceId}
			options={[
				{ value: '', label: m.models_all_spaces() },
				...spaces.map((space) => ({ value: space.id, label: space.name }))
			]}
			onchange={load}
		/>
	</div>

	{#if loading}
		<div class="flex flex-col gap-2" role="status" aria-label={m.models_loading()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title={m.models_load_failed()} message={error} onretry={load} />
	{:else if visible.length === 0}
		<EmptyState title={m.models_empty()} description={m.models_empty_hint()} />
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[11px]">
				<thead class="border-b border-border text-[10px] uppercase tracking-wide text-text-muted">
					<tr>
						<th class="px-3 py-2">{m.models_col_model()}</th>
						<th class="px-3 py-2">{m.models_col_source()}</th>
						<th class="px-3 py-2">{m.models_col_fields()}</th>
						<th class="px-3 py-2">{m.common_space()}</th>
						<th class="px-3 py-2">{m.models_col_updated()}</th>
					</tr>
				</thead>
				<tbody>
					{#each visible as model (model.id)}
						{@const def = definitionOf(model)}
						<tr class="border-b border-border/60 last:border-b-0 hover:bg-bg-toolbar">
							<td class="px-3 py-2">
								<a class="font-medium hover:underline" href={resolve(`/models/${model.id}`)}
									>{model.label}</a
								>
								{#if model.certified}<Badge tone="success">{m.models_certified()}</Badge>{/if}
								<div class="font-mono text-[10px] text-text-muted">{model.slug}</div>
							</td>
							<td class="px-3 py-2 font-mono">{source(model)}</td>
							<td class="px-3 py-2">
								{m.models_field_counts({
									dimensions: def.dimensions?.length ?? 0,
									metrics: def.metrics?.length ?? 0,
									joins: def.joins?.length ?? 0
								})}
							</td>
							<td class="px-3 py-2">
								{spaces.find((space) => space.id === model.space_id)?.name ?? '—'}
							</td>
							<td class="px-3 py-2"><RelativeTime at={model.updated_at} /></td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

<MigrateV1Dialog bind:open={migrateOpen} {workspaceId} onmigrated={load} />
