// URL-backed selection (#29). The workspace, catalog, namespaces, table and
// active tab live in the query string so a reload or a shared link restores the
// same view.
import { goto } from '$app/navigation';
import { page } from '$app/state';
import { applySelection, type SelectionPatch } from './selectionUrl';

export type Selection = {
	ws: string;
	catalog: string;
	ns: string[];
	table: string;
	tab: string;
};

function createSelectionStore() {
	const ws = $derived(page.url.searchParams.get('ws') ?? '');
	const catalog = $derived(page.url.searchParams.get('catalog') ?? '');
	const ns = $derived(page.url.searchParams.getAll('ns'));
	const table = $derived(page.url.searchParams.get('table') ?? '');
	const tab = $derived(page.url.searchParams.get('tab') ?? '');

	function set(patch: SelectionPatch) {
		// The pathname is dynamic, so resolve() cannot type it (#29).
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(applySelection(page.url, patch), {
			replaceState: true,
			keepFocus: true,
			noScroll: true
		});
	}

	return {
		get ws() {
			return ws;
		},
		get catalog() {
			return catalog;
		},
		get ns() {
			return ns;
		},
		get table() {
			return table;
		},
		get tab() {
			return tab;
		},
		set
	};
}

export const selection = createSelectionStore();
