// "Insert from model" palette provider (#121). It registers through the same
// registry as the other providers (#112): a model's dimension or metric is
// compiled to SQL by the API and inserted at the cursor.
import { m } from '$lib/paraglide/messages';
import type { PaletteProvider } from '$lib/palette/registry';
import { compileExplore, definitionOf, listModels, type ExploreQuery } from './explore';

export function modelPaletteProvider(options: {
	workspaceId: () => string;
	bindingId: () => string;
	insert: (sql: string) => void;
}): PaletteProvider {
	return {
		id: 'models',
		label: m.semantic_palette_models(),
		search: async (query) => {
			const workspaceId = options.workspaceId();
			if (!workspaceId) return [];
			const needle = query.trim().toLowerCase();
			const models = await listModels(workspaceId);
			const items: Awaited<ReturnType<PaletteProvider['search']>> = [];
			for (const model of models) {
				const def = definitionOf(model);
				const fields = [
					...(def.dimensions ?? [])
						.filter((d) => !d.hidden)
						.map((d) => ({ name: d.name, label: d.label || d.name, kind: 'dimensions' as const })),
					...(def.metrics ?? []).map((mt) => ({
						name: mt.name,
						label: mt.label || mt.name,
						kind: 'metrics' as const
					}))
				];
				for (const field of fields) {
					const haystack =
						`${model.slug} ${model.label} ${field.name} ${field.label}`.toLowerCase();
					if (needle && !haystack.includes(needle)) continue;
					items.push({
						id: `model-${model.id}-${field.name}`,
						label: field.label,
						detail: `${model.label} · ${field.kind === 'metrics' ? 'metric' : 'dimension'}`,
						group: 'Models',
						run: async () => {
							const explore: ExploreQuery = { model: model.slug, [field.kind]: [field.name] };
							const out = await compileExplore(workspaceId, explore, options.bindingId() || null);
							options.insert(out.sql);
						}
					});
					if (items.length >= 20) return items;
				}
			}
			return items;
		}
	};
}
