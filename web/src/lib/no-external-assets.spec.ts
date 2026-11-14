import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// Nothing the app needs to render may come from outside the bundle: no CDN
// fonts, scripts, stylesheets or icon sets. Self-host them (Fontsource, npm).
// A scheme-relative URL (//host/x) still picks an external host, so it counts.
const HOST = String.raw`(?:https?:)?\/\/`;
const EXTERNAL = [
	new RegExp(String.raw`<link\b[^>]*\bhref\s*=\s*["']?${HOST}`, 'i'),
	new RegExp(String.raw`<script\b[^>]*\bsrc\s*=\s*["']?${HOST}`, 'i'),
	new RegExp(String.raw`@import\s+(?:url\()?\s*["']?${HOST}`, 'i'),
	new RegExp(String.raw`\burl\(\s*["']?${HOST}`, 'i')
];

function loadsExternally(text: string): boolean {
	return EXTERNAL.some((pattern) => pattern.test(text));
}

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
		const offenders = files('src').filter((file) => loadsExternally(readFileSync(file, 'utf8')));
		expect(offenders).toEqual([]);
	});

	// The guard itself: each kind of reference, with an explicit and a
	// scheme-relative host, must be refused.
	it.each([
		['link, https', '<link rel="stylesheet" href="https://fonts.googleapis.com/css2">'],
		['link, http', '<link rel="stylesheet" href="http://cdn.example.com/a.css">'],
		['link, scheme-relative', '<link rel="stylesheet" href="//cdn.example.com/site.css">'],
		['script, https', '<script src="https://cdn.example.com/a.js"></script>'],
		['script, scheme-relative', '<script src="//cdn.example.com/a.js"></script>'],
		['@import, https', '@import url("https://cdn.example.com/a.css");'],
		['@import, bare string', "@import '//cdn.example.com/a.css';"],
		['@import, scheme-relative url()', '@import url(//cdn.example.com/a.css);'],
		['url(), https', '.a { background: url(https://cdn.example.com/a.png); }'],
		['url(), scheme-relative', ".a { background: url('//cdn.example.com/a.png'); }"]
	])('refuses %s', (_name, text) => {
		expect(loadsExternally(text)).toBe(true);
	});

	it('accepts what ships with the app', () => {
		for (const text of [
			'<link rel="icon" href="%sveltekit.assets%/favicon.svg" />',
			'<link rel="manifest" href="/site.webmanifest" />',
			'<a href="https://lucide.dev" target="_blank">Lucide</a>',
			'.a { background: url(/img/a.png); }',
			"@import './local.css';"
		]) {
			expect(loadsExternally(text), text).toBe(false);
		}
	});
});
