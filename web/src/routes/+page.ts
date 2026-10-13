import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Not prerendered: the load below needs the live server.
export const prerender = false;
export const ssr = false;

export const load: PageLoad = async ({ fetch }) => {
	let configured = true;
	try {
		const res = await fetch('/api/v1/setup/status');
		if (res.ok) configured = (await res.json()).is_configured !== false;
	} catch {
		// Server unreachable: fall through to the normal flow instead of trapping the user on /setup.
	}
	if (!configured) redirect(307, '/setup');
};
