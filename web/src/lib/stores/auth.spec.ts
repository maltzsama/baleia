import { describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { makeMe } from '../../test/factories';
import { authStore } from './auth.svelte';

describe('authStore', () => {
	// Settings render their page only while not loading; a page that reloads
	// the user used to flip `loading` and remount itself in a loop.
	it('keeps the loaded user on screen while it refreshes', async () => {
		server.use(http.get('/auth/me', ({ response }) => response(200).json(makeMe())));
		await authStore.load();
		expect(authStore.loading).toBe(false);

		const refresh = authStore.load();
		expect(authStore.loading).toBe(false);
		expect(authStore.user?.name).toBe('Ada Lovelace');
		await refresh;
	});
});
