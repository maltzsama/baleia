<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Workbench from '$lib/components/Workbench.svelte';
	import { editor } from '$lib/stores/editor.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	let error = $state('');

	onMount(async () => {
		await workspaceStore.load();
		const workspaceId = workspaceStore.current?.id;
		if (!workspaceId) {
			error = 'Select a workspace to open this query.';
			return;
		}
		const queryId = page.params.query_id;
		if (!queryId) {
			error = 'This saved query link is incomplete.';
			return;
		}
		const { data, error: err } = await client.GET('/workspaces/{id}/queries/{query_id}', {
			params: { path: { id: workspaceId, query_id: queryId } }
		});
		if (err || !data) {
			error = apiErrorMessage(err, 'Failed to open the saved query');
			return;
		}
		editor.openSavedQuery(data);
	});
</script>

{#if error}
	<div class="flex-1 p-7">
		<p class="text-sm text-danger">{error}</p>
	</div>
{:else}
	<Workbench />
{/if}
