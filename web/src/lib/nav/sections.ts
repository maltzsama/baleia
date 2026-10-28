// Navigation areas (#464, #465): one entry per area of the product, each with
// the pages it groups. The rail, the area tabs and the header title read this
// one map, so a page is named the same everywhere. Routes do not change: a
// page's href is the route it already had.
import { m } from '$lib/paraglide/messages';
import { sectionForPath } from '$lib/settings/sections';
import type { Features } from '$lib/stores/auth.svelte';

export type AreaKey =
	'sql' | 'data' | 'explore' | 'content' | 'connections' | 'monitoring' | 'settings';

export type NavPage = {
	href: string;
	label: () => string;
	/** Shown only when this /auth/me feature flag is on. */
	feature?: keyof Features;
};

export type NavArea = {
	key: AreaKey;
	label: () => string;
	/** Where the area opens: its first page. */
	href: string;
	pages: NavPage[];
};

export const navAreas: NavArea[] = [
	{
		key: 'sql',
		label: m.nav_sql,
		href: '/sql',
		pages: [
			{ href: '/sql', label: m.nav_page_editor },
			{ href: '/history', label: m.nav_page_history }
		]
	},
	{
		key: 'data',
		label: m.nav_data,
		href: '/data',
		pages: [
			{ href: '/data', label: m.nav_page_explorer },
			{ href: '/data/docs', label: m.nav_page_docs }
		]
	},
	{
		key: 'explore',
		label: m.nav_explore,
		href: '/explore',
		pages: [
			{ href: '/explore', label: m.nav_page_explore },
			{ href: '/models', label: m.nav_page_models }
		]
	},
	{
		key: 'content',
		label: m.nav_content,
		href: '/content',
		pages: [
			{ href: '/content', label: m.nav_page_spaces },
			{ href: '/metrics', label: m.nav_page_metrics },
			{ href: '/retrievers', label: m.nav_page_retrievers, feature: 'retrievers' },
			{ href: '/content/trash', label: m.nav_page_trash }
		]
	},
	{
		key: 'connections',
		label: m.nav_connections,
		href: '/connections',
		pages: [
			{ href: '/connections', label: m.nav_page_connections },
			{ href: '/catalogs', label: m.nav_page_catalogs },
			{ href: '/engines', label: m.nav_page_engines }
		]
	},
	{
		key: 'monitoring',
		label: m.nav_monitoring,
		href: '/monitoring',
		pages: [{ href: '/monitoring', label: m.nav_monitoring }]
	},
	// Settings keeps its own section nav (lib/settings/sections.ts).
	{ key: 'settings', label: m.nav_settings, href: '/settings', pages: [] }
];

function under(pathname: string, href: string): boolean {
	return pathname === href || pathname.startsWith(`${href}/`);
}

/** The pages of an area the user can see (feature flags applied). */
export function visiblePages(area: NavArea, features: Features | undefined): NavPage[] {
	return area.pages.filter((page) => !page.feature || features?.[page.feature]);
}

/**
 * The area and page a path belongs to. The longest matching page wins, so
 * /data/docs is Docs (not Explorer) and /content/trash is Trash.
 */
export function locate(pathname: string): { area: NavArea; page: NavPage | null } | null {
	let best: { area: NavArea; page: NavPage } | null = null;
	for (const area of navAreas) {
		for (const page of area.pages) {
			if (under(pathname, page.href) && (!best || page.href.length > best.page.href.length)) {
				best = { area, page };
			}
		}
	}
	if (best) return best;
	const area = navAreas.find((item) => under(pathname, item.href));
	return area ? { area, page: null } : null;
}

/**
 * The header title for a path: the area, plus the page when the area has
 * several (SQL · History, Data · Docs), or the settings section.
 */
export function sectionTitle(pathname: string): string {
	const found = locate(pathname);
	if (!found) return '';
	const { area, page } = found;
	if (area.key === 'settings') {
		const section = sectionForPath(pathname);
		return section ? `${area.label()} · ${section.label}` : area.label();
	}
	if (!page || area.pages.length < 2 || page.label() === area.label()) return area.label();
	return `${area.label()} · ${page.label()}`;
}
