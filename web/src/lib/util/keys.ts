// Keyboard shortcut labels (#473). Shortcuts are written in CodeMirror's
// notation (Mod-Enter, Shift-Mod-S); this renders them for the platform the
// page runs on: ⇧⌘↵ on Apple devices, Shift+Ctrl+Enter elsewhere. The
// handlers stay the same: Mod is Meta on macOS and Ctrl everywhere else.

/** True on macOS and iOS, where Mod is ⌘. */
export function isApplePlatform(nav: Navigator | undefined = globalThis.navigator): boolean {
	if (!nav) return false;
	const data = (nav as Navigator & { userAgentData?: { platform?: string } }).userAgentData;
	const platform = data?.platform || nav.platform || nav.userAgent;
	return /mac|iphone|ipad|ipod/i.test(platform);
}

const apple: Record<string, string> = {
	Mod: '⌘',
	Shift: '⇧',
	Alt: '⌥',
	Ctrl: '⌃',
	Enter: '↵'
};
const other: Record<string, string> = { Mod: 'Ctrl', Enter: 'Enter' };

/** "Shift-Mod-Enter" → "⇧⌘↵" on Apple, "Shift+Ctrl+Enter" elsewhere. */
export function formatKeys(keys: string, applePlatform = isApplePlatform()): string {
	const parts = keys.split('-');
	if (applePlatform) return parts.map((part) => apple[part] ?? part).join('');
	return parts.map((part) => other[part] ?? part).join('+');
}
