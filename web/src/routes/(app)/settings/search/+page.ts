import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// The search glossary moved next to the models.
export const load: PageLoad = () => {
	redirect(308, '/models/glossary');
};
