// Command palette providers (#112). New integrations (for example "Insert
// metric" from #115) register a provider instead of editing the palette.
export type PaletteItem = {
	id: string;
	label: string;
	detail?: string;
	group: string;
	run: () => void | Promise<void>;
};

export type PaletteProvider = {
	id: string;
	label: string;
	search: (query: string) => PaletteItem[] | Promise<PaletteItem[]>;
};

const providers = new Map<string, PaletteProvider>();

export function registerProvider(provider: PaletteProvider): void {
	providers.set(provider.id, provider);
}

export function unregisterProvider(id: string): void {
	providers.delete(id);
}

export function providersList(): PaletteProvider[] {
	return [...providers.values()];
}

/** Tests call this between cases. */
export function resetProviders(): void {
	providers.clear();
}
