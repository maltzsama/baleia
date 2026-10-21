// Content kind registry (#113, #121). Routes and labels per kind live here so a
// new kind (the semantic `model`) is one more entry, not a branch per screen.
export type ContentKind = 'folder' | 'saved_query' | 'semantic_metric' | 'semantic_model';

export type ContentKindEntry = {
	label: string;
	tone: 'neutral' | 'info';
	href: (id: string) => string;
};

export const contentKinds: Record<ContentKind, ContentKindEntry> = {
	folder: { label: 'Folder', tone: 'neutral', href: (id) => `/content?folder=${id}` },
	saved_query: { label: 'Query', tone: 'neutral', href: (id) => `/sql/q/${id}` },
	semantic_metric: { label: 'Metric', tone: 'info', href: (id) => `/metrics/${id}` },
	semantic_model: { label: 'Model', tone: 'info', href: (id) => `/models/${id}` }
};

export function kindEntry(kind: string): ContentKindEntry {
	return contentKinds[kind as ContentKind] ?? contentKinds.folder;
}
