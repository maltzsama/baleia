import { redirect } from '@sveltejs/kit';
import { clearSession, client } from '$lib/api/client';
import { bootstrap } from '$lib/auth/session';
import { initLocale } from '$lib/i18n';
import type { LayoutLoad } from './$types';

export const ssr = false;

export const load: LayoutLoad = async ({ url }) => {
	if (typeof window === 'undefined') return { user: null };

	// Restore the session from the HttpOnly cookie before asking for the user.
	// The refresh is serialized across tabs (#263).
	if ((await bootstrap()) === 'anonymous') {
		clearSession();
		const next = `${url.pathname}${url.search}`;
		redirect(307, `/login?next=${encodeURIComponent(next)}`);
	}

	// A network failure must land on the login page, not an unhandled rejection.
	const { data } = await client.GET('/auth/me').catch(() => ({ data: undefined }));
	if (!data) {
		clearSession();
		const next = `${url.pathname}${url.search}`;
		redirect(307, `/login?next=${encodeURIComponent(next)}`);
	}
	initLocale((data as { locale?: string | null }).locale);
	return { user: data };
};
