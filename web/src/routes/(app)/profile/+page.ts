// src/routes/(app)/profile/+page.ts
import { client } from '$lib/api/client';
import type { PageLoad } from './$types';

export const load: PageLoad = async () => {
	if (typeof localStorage === 'undefined') return { user: null };
	const { data } = await client.GET('/auth/me');
	return { user: data ?? null };
};
