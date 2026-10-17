<script lang="ts">
	import { apiErrorMessage, client } from '$lib/api/client';
	import type { components } from '$lib/api/schema';
	import type { EditorTab } from '$lib/stores/editor.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import DiffView from '$lib/ui/patterns/DiffView.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import type { Conflict } from './conflict';

	let {
		open = $bindable(false),
		tab,
		workspaceId,
		conflict,
		onsaved,
		oncopy
	}: {
		open?: boolean;
		tab: EditorTab;
		workspaceId: string;
		conflict: Conflict;
		onsaved: (query: components['schemas']['SavedQuery']) => void;
		oncopy: () => void;
	} = $props();

	let compare = $state(false);
	let busy = $state(false);
	let error = $state('');

	async function overwrite() {
		if (!tab.savedRef) return;
		busy = true;
		error = '';
		try {
			const body: components['schemas']['UpdateSavedQueryRequest'] = {
				name: tab.name,
				slug: tab.savedRef.slug,
				space_id: tab.savedRef.space_id,
				sql: tab.sql,
				target: tab.bindingId
					? { binding_id: tab.bindingId, ...(tab.schema ? { schema: tab.schema } : {}) }
					: undefined,
				expected_version: conflict.currentVersion
			};
			if (tab.savedRef.folder_id) body.folder_id = tab.savedRef.folder_id;
			else body.move_to_root = true;
			const { data, error: err } = await client.PATCH('/workspaces/{id}/queries/{query_id}', {
				params: { path: { id: workspaceId, query_id: tab.savedRef.id } },
				body
			});
			if (err || !data) {
				error = apiErrorMessage(err, 'Could not overwrite the query');
				return;
			}
			savedQueries.onSaved(data);
			open = false;
			onsaved(data);
		} finally {
			busy = false;
		}
	}
</script>

<Modal bind:open title="This query changed elsewhere">
	<div class="flex flex-col gap-3 text-[13px]">
		<p class="text-slate-600 dark:text-slate-300">
			{#if conflict.updatedBy?.name}
				<span class="font-semibold text-text-main">{conflict.updatedBy.name}</span>
			{:else}
				Someone
			{/if}
			saved version {conflict.currentVersion}
			{#if conflict.updatedAt}
				<RelativeTime at={conflict.updatedAt} />
			{/if}
			while you were editing.
		</p>
		<Button size="sm" onclick={() => (compare = !compare)}>
			{compare ? 'Hide comparison' : 'Compare'}
		</Button>
		{#if compare}
			<DiffView before={conflict.theirs.sql} after={tab.sql} language="sql" />
		{/if}
		{#if error}
			<p class="text-danger" role="alert">{error}</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex flex-wrap justify-end gap-2">
			<Button onclick={() => (open = false)} disabled={busy}>Cancel</Button>
			<Button
				onclick={() => {
					open = false;
					oncopy();
				}}
				disabled={busy}
			>
				Save mine as a copy
			</Button>
			<Button variant="danger" loading={busy} disabled={busy} onclick={overwrite}>Overwrite</Button>
		</div>
	{/snippet}
</Modal>
