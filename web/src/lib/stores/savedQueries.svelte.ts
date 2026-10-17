// Saved-query list state (#29). `onSaved` keeps the list coherent after the
// workbench saves without a window event.
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type SavedQuery = components['schemas']['SavedQuery'];

function createSavedQueriesStore() {
	let list = $state<SavedQuery[]>([]);
	let loading = $state(false);
	let error = $state('');
	let workspaceId = $state('');

	async function reload(id = workspaceId) {
		workspaceId = id;
		if (!id) {
			list = [];
			loading = false;
			return;
		}
		loading = true;
		error = '';
		const { data, error: err } = await client.GET('/workspaces/{id}/queries', {
			params: { path: { id } }
		});
		if (err) error = apiErrorMessage(err, 'Failed to load saved queries');
		list = data ?? [];
		loading = false;
	}

	function onSaved(query: SavedQuery) {
		const index = list.findIndex((item) => item.id === query.id);
		if (index >= 0) list[index] = query;
		else list = [...list, query];
	}

	return {
		get list() {
			return list;
		},
		get loading() {
			return loading;
		},
		get error() {
			return error;
		},
		get workspaceId() {
			return workspaceId;
		},
		reload,
		onSaved
	};
}

export const savedQueries = createSavedQueriesStore();
