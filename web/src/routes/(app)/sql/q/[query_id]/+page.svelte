<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Workbench from '$lib/components/Workbench.svelte';
	import { editor } from '$lib/stores/editor.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';

	// The workbench stays mounted (its drafts hydrate as usual) while the
	// saved query loads (#470): a status bar says so, and the query opens in
	// its own tab, so a late answer never replaces what the user typed.
	let opening = $state(true);
	let error = $state('');

	async function open() {
		opening = true;
		error = '';
		try {
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
		} catch (e) {
			error = apiErrorMessage(e, m.sql_q_failed_to_open_the_saved_query());
		} finally {
			opening = false;
		}
	}

	onMount(open);
</script>

{#if error}
	<div class="flex flex-1 flex-col items-start gap-3 p-7">
		<ErrorState title={m.sql_q_failed_to_open_the_saved_query()} message={error} onretry={open} />
		<a href={resolve('/sql')} class="text-[12px] font-semibold text-primary hover:underline">
			{m.sql_q_back_to_the_workbench()}
		</a>
	</div>
{:else}
	{#if opening}
		<div
			role="status"
			class="shrink-0 border-b border-border bg-bg-toolbar px-4 py-1.5 text-[11px] text-text-muted"
		>
			{m.sql_q_opening_the_saved_query()}
		</div>
	{/if}
	<Workbench />
{/if}
