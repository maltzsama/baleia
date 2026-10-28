// A reactive stand-in for `page` from `$app/state` in page tests. Stores that
// derive from `page.url` (selection) are module singletons, so a plain mock
// would keep the first URL they read; this one tracks what a test sets.
import { SvelteURL } from 'svelte/reactivity';

const url = new SvelteURL('http://localhost/');

export const testPage = {
	get url() {
		return url;
	},
	params: {} as Record<string, string>
};

/** Points the page at a path (or an absolute URL) for the next render. */
export function setTestUrl(next: string) {
	url.href = next.startsWith('http') ? next : `http://localhost${next}`;
}
