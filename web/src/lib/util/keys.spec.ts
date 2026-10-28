import { describe, expect, it } from 'vitest';
import { formatKeys, isApplePlatform } from './keys';

// #473: shortcut labels follow the platform; the handlers do not change.
describe('keys', () => {
	it.each([
		['Mod-Enter', '⌘↵', 'Ctrl+Enter'],
		['Shift-Mod-Enter', '⇧⌘↵', 'Shift+Ctrl+Enter'],
		['Shift-Alt-F', '⇧⌥F', 'Shift+Alt+F'],
		['Alt-N', '⌥N', 'Alt+N']
	])('formats %s as %s on Apple and %s elsewhere', (keys, apple, other) => {
		expect(formatKeys(keys, true)).toBe(apple);
		expect(formatKeys(keys, false)).toBe(other);
	});

	it.each([
		[{ platform: 'MacIntel', userAgent: '' }, true],
		[{ platform: 'iPhone', userAgent: '' }, true],
		[{ platform: 'Win32', userAgent: '' }, false],
		[{ platform: 'Linux x86_64', userAgent: '' }, false],
		[{ platform: '', userAgent: '', userAgentData: { platform: 'macOS' } }, true],
		[{ platform: '', userAgent: '', userAgentData: { platform: 'Windows' } }, false]
	])('detects the platform of %o', (nav, apple) => {
		expect(isApplePlatform(nav as unknown as Navigator)).toBe(apple);
	});
});
