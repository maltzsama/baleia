// The command palette's search provider (#943): one ranked list over tables,
// columns, docs, saved queries, metrics, models and folders, from the
// workspace search. It registers through the same registry as the other
// providers (#112); the editor says what each hit does inside it.
import { m } from '$lib/paraglide/messages';
import type { PaletteItem, PaletteProvider } from '$lib/palette/registry';
import { searchWorkspace, type WorkspaceSearchItem } from './client';
import { qualifiedName, searchItemHref, searchKindLabel } from './items';

/** Hits the palette asks for; the palette lists them in the server's order. */
export const paletteSearchLimit = 20;

export function searchPaletteProvider(options: {
	workspaceId: () => string;
	/** Inserts text at the editor's cursor. */
	insert: (text: string) => void;
	/** Opens a saved query in the editor; false when the editor does not know it. */
	openSavedQuery: (id: string) => boolean | Promise<boolean>;
	/** Inserts the SQL a metric renders to. */
	insertMetric: (id: string) => Promise<void>;
	/** Goes to a page of the app. */
	navigate: (href: string) => void;
}): PaletteProvider {
	async function run(item: WorkspaceSearchItem): Promise<void> {
		const href = searchItemHref(item);
		switch (item.kind) {
			case 'table':
				options.insert(qualifiedName(item));
				return;
			case 'column':
				options.insert(item.metadata?.column ?? item.title);
				return;
			case 'semantic_metric':
				await options.insertMetric(item.id);
				return;
			case 'saved_query':
				if (await options.openSavedQuery(item.id)) return;
		}
		if (href) options.navigate(href);
	}

	return {
		id: 'search',
		label: m.common_results(),
		search: async (query) => {
			const q = query.trim();
			const workspaceId = options.workspaceId();
			if (q.length < 2 || !workspaceId) return [];
			const found = await searchWorkspace(workspaceId, q, { limit: paletteSearchLimit });
			return found.items.map((item): PaletteItem => ({
				id: `${item.kind}-${item.id}`,
				label: item.title,
				detail: item.subtitle ?? undefined,
				group: searchKindLabel(item.kind),
				run: () => run(item)
			}));
		}
	};
}
