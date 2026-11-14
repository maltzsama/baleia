import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// Nothing the app needs to render may come from outside the bundle: no CDN
// fonts, scripts, stylesheets or icon sets. Self-host them (Fontsource, npm).
const EXTERNAL = [
	/<link\b[^>]*\bhref\s*=\s*["']?https?:\/\//i,
	/<script\b[^>]*\bsrc\s*=\s*["']?https?:\/\//i,
	/@import\s+(?:url\()?\s*["']?https?:\/\//i,
	/\burl\(\s*["']?https?:\/\//i
];

function files(dir: string, found: string[] = []): string[] {
	for (const entry of readdirSync(dir, { withFileTypes: true })) {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) {
			if (entry.name !== 'paraglide') files(path, found);
		} else if (/\.(html|css|svelte|ts)$/.test(entry.name) && !/\.spec\.ts$/.test(entry.name)) {
			found.push(path);
		}
	}
	return found;
}

describe('self-hosted assets', () => {
	it('loads no font, script, stylesheet or image from an external host', () => {
		const offenders = files('src').filter((file) => {
			const text = readFileSync(file, 'utf8');
			return EXTERNAL.some((pattern) => pattern.test(text));
		});
		expect(offenders).toEqual([]);
	});
});
