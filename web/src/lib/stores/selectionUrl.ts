// Pure URL math for the selection store (#29). Kept separate from $app/state so
// it can be unit tested without a SvelteKit runtime.

export type SelectionPatch = {
	ws?: string;
	catalog?: string;
	ns?: string[];
	table?: string;
	tab?: string;
};

export function applySelection(current: URL, patch: SelectionPatch): string {
	const params = new URLSearchParams(current.searchParams);

	if (patch.ws !== undefined) {
		if (patch.ws) params.set('ws', patch.ws);
		else params.delete('ws');
	}
	if (patch.catalog !== undefined) {
		if (patch.catalog) params.set('catalog', patch.catalog);
		else params.delete('catalog');
	}
	if (patch.ns !== undefined) {
		params.delete('ns');
		for (const value of patch.ns) params.append('ns', value);
	}
	if (patch.table !== undefined) {
		if (patch.table) params.set('table', patch.table);
		else params.delete('table');
	}
	if (patch.tab !== undefined) {
		if (patch.tab) params.set('tab', patch.tab);
		else params.delete('tab');
	}

	const query = params.toString();
	return `${current.pathname}${query ? `?${query}` : ''}`;
}
