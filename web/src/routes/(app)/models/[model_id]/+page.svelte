<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import HistoryDrawer from '$lib/content/HistoryDrawer.svelte';
	import ModelEditor from '$lib/semantic/ModelEditor.svelte';
	import {
		deleteModel,
		encodeShare,
		getModel,
		definitionOf,
		type SemanticModel
	} from '$lib/semantic/explore';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import CodeBlock from '$lib/ui/patterns/CodeBlock.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const modelId = $derived(page.params.model_id ?? '');

	let model = $state<SemanticModel | null>(null);
	let loading = $state(true);
	let error = $state('');
	let historyOpen = $state(false);
	let deleteOpen = $state(false);
	// Bumped after a save or a restore so the editor remounts on the new version.
	let editorKey = $state(0);

	const canEdit = $derived(!!model?.allowed_actions?.includes('update'));
	const exploreHref = $derived.by(() => {
		if (!model) return resolve('/explore');
		const def = definitionOf(model);
		const dimension = def.dimensions?.find((d) => !d.hidden)?.name;
		const metric = def.metrics?.[0]?.name;
		return `${resolve('/explore')}?q=${encodeShare({
			model: model.slug,
			dimensions: dimension ? [dimension] : undefined,
			metrics: metric ? [metric] : undefined
		})}`;
	});

	async function load() {
		if (!workspaceId || !modelId) return;
		loading = true;
		error = '';
		try {
			await savedQueries.reload(workspaceId);
			model = await getModel(workspaceId, modelId);
			editorKey++;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function remove() {
		if (!model) return;
		await deleteModel(workspaceId, model.id);
		await goto(resolve('/models'));
	}

	onMount(async () => {
		await workspaceStore.load();
		await load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	{#if loading && !model}
		<Skeleton class="h-32 w-full" />
	{:else if error && !model}
		<ErrorState title={m.models_load_one_failed()} message={error} onretry={load} />
	{:else if model}
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div>
				<div class="flex flex-wrap items-center gap-2">
					<h1 class="text-xl font-semibold tracking-[-0.3px]">{model.label}</h1>
					{#if model.certified}<Badge tone="success">{m.models_certified()}</Badge>{/if}
					{#if !canEdit}<Badge tone="neutral">{m.models_read_only()}</Badge>{/if}
				</div>
				<p class="mt-1 flex items-center gap-1 font-mono text-[11px] text-text-muted">
					{model.slug}<CopyButton value={model.slug} label="slug" />
					· {savedQueries.spaces.find((space) => space.id === model?.space_id)?.name ?? '—'}
					· v{model.version} · <RelativeTime at={model.updated_at} />
				</p>
			</div>
			<div class="flex flex-wrap gap-2">
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- exploreHref is resolve('/explore') plus the query -->
				<Button onclick={() => goto(exploreHref)}>{m.models_explore()}</Button>
				<Button onclick={() => (historyOpen = true)}>{m.models_history()}</Button>
				{#if canEdit}
					<Button variant="danger" onclick={() => (deleteOpen = true)}>{m.common_delete()}</Button>
				{/if}
			</div>
		</div>

		{#if canEdit}
			{#key editorKey}
				<ModelEditor {model} onsaved={load} />
			{/key}
		{:else}
			<CodeBlock
				code={JSON.stringify(model.definition, null, 2)}
				language="json"
				label={m.models_definition()}
				maxHeight="640px"
			/>
		{/if}
	{/if}
</div>

<HistoryDrawer
	bind:open={historyOpen}
	kind="semantic_model"
	entityId={modelId}
	{workspaceId}
	onrestored={load}
/>

<ConfirmDialog
	bind:open={deleteOpen}
	title={m.models_delete_title()}
	resourceName={model?.label ?? ''}
	confirmLabel={m.models_delete_confirm()}
	consequences={[m.models_delete_consequence_explores(), m.models_delete_consequence_trash()]}
	onconfirm={remove}
/>
