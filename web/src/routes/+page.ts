import { redirect } from '@sveltejs/kit';
import { client } from '$lib/api/client';
import type { PageLoad } from './$types';

// Not prerendered: the load below needs the live server.
export const prerender = false;
export const ssr = false;

export const load: PageLoad = async () => {
	let configured = true;
	try {
		const { data } = await client.GET('/setup/status');
		if (data) configured = data.is_configured !== false;
	} catch {
		// Server unreachable: fall through to the normal flow instead of trapping the user on /setup.
	}
	if (!configured) redirect(307, '/setup');
};
