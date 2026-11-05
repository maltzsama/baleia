import { redirect } from '@sveltejs/kit';
import { clearSession } from '$lib/api/client';
import { bootstrap } from '$lib/auth/session';

/**
 * Steps 2 and 3 of the first-run setup need the admin's session. Restore it
 * from the HttpOnly cookie, so a refresh stays on the step, or sign in and come
 * back to it.
 */
export async function requireSetupSession(pathname: string): Promise<void> {
	if (typeof window === 'undefined') return;
	if ((await bootstrap()) === 'anonymous') {
		clearSession();
		redirect(307, `/login?next=${encodeURIComponent(pathname)}`);
	}
}
