import { redirect } from '@sveltejs/kit';
import { firstVisibleSection } from '$lib/settings/sections';
import type { PageLoad } from './$types';

// The landing page has no content of its own: it resolves to the first section
// the caller may open (#262). The server remains the source of truth.
export const prerender = false;
export const ssr = false;

export const load: PageLoad = async ({ parent }) => {
	const { user } = await parent();
	const section = firstVisibleSection(user);
	redirect(307, section?.href ?? '/sql');
};
