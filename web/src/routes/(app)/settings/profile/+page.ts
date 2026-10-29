import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Personal pages left Settings, which is administration only.
export const prerender = false;
export const ssr = false;

export const load: PageLoad = () => {
	redirect(308, '/profile');
};
