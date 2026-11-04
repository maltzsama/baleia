import { requireSetupSession } from '$lib/setup/session';
import type { PageLoad } from './$types';

export const ssr = false;

export const load: PageLoad = async ({ url }) => {
	await requireSetupSession(url.pathname);
};
