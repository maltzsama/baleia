import { redirect } from '@sveltejs/kit';
import { clearSession, client } from '$lib/api/client';
import type { LayoutLoad } from './$types';

export const ssr = false;

export const load: LayoutLoad = async ({ url }) => {
	if (typeof window === 'undefined') return { user: null };
	const { data } = await client.GET('/auth/me');
	if (!data) {
		// Drop the rejected session here: leaving it in localStorage makes the
		// login page bounce straight back to /sql (a redirect loop).
		clearSession();
		const next = `${url.pathname}${url.search}`;
		redirect(307, `/login?next=${encodeURIComponent(next)}`);
	}
	return { user: data };
};
