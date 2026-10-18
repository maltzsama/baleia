import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// The profile moved into the settings area (#287).
export const prerender = false;
export const ssr = false;

export const load: PageLoad = () => {
	redirect(307, '/settings/profile');
};
