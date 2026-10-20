<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Workbench from '$lib/components/Workbench.svelte';
	import { editor } from '$lib/stores/editor.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	let error = $state('');

	onMount(async () => {
		await workspaceStore.load();
		const workspaceId = workspaceStore.current?.id;
		if (!workspaceId) {
			error = m.sql_q_select_a_workspace_to_open_this_query();
			return;
		}
		const queryId = page.params.query_id;
		if (!queryId) {
			error = m.sql_q_this_saved_query_link_is_incomplete();
			return;
		}
		const { data, error: err } = await client.GET('/workspaces/{id}/queries/{query_id}', {
			params: { path: { id: workspaceId, query_id: queryId } }
		});
		if (err || !data) {
			error = apiErrorMessage(err, m.sql_q_failed_to_open_the_saved_query());
			return;
		}
		await savedQueries.reload(workspaceId);
		editor.openSavedQuery(data, !savedQueries.canEdit(data.space_id));
	});
</script>

{#if error}
	<div class="flex-1 p-7">
		<p class="text-sm text-danger">{error}</p>
	</div>
{:else}
	<Workbench />
{/if}
