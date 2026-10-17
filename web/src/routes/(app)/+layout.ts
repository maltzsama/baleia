import { redirect } from '@sveltejs/kit';
import { client } from '$lib/api/client';
import type { LayoutLoad } from './$types';

export const ssr = false;

export const load: LayoutLoad = async ({ url }) => {
	if (typeof localStorage === 'undefined') return { user: null };
	const { data } = await client.GET('/auth/me');
	if (!data) {
		const next = `${url.pathname}${url.search}`;
		redirect(307, `/login?next=${encodeURIComponent(next)}`);
	}
	return { user: data };
};
