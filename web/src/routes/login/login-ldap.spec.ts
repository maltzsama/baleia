import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import LoginPage from './+page.svelte';

const oidcId = '00000000-0000-0000-0000-0000000000a1';
const ldapId = '00000000-0000-0000-0000-0000000000a2';

describe('login page with providers', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/login');
	});

	it('lists OIDC buttons and switches to LDAP username mode', async () => {
		let loginBody: unknown = null;
		server.use(
			http.get('/auth/providers', ({ response }) =>
				response(200).json([
					{ id: oidcId, name: 'Okta', type: 'oidc' },
					{ id: ldapId, name: 'Corp LDAP', type: 'ldap' }
				])
			),
			http.post('/auth/login', async ({ request, response }) => {
				loginBody = await request.json();
				return response(401).json({ code: 'invalid_credentials', message: 'nope' });
			}),
			http.get('/auth/me', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'no session' })
			),
			http.post('/auth/refresh', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'no session' })
			)
		);

		render(LoginPage);

		expect(await screen.findByRole('button', { name: 'Continue with Okta' })).toBeTruthy();

		const select = await screen.findByLabelText('Sign in with');
		await fireEvent.change(select, { target: { value: ldapId } });

		const username = await screen.findByLabelText('Username');
		await fireEvent.input(username, { target: { value: 'alice' } });
		await fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'secret' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));

		await waitFor(() =>
			expect(loginBody).toEqual({ provider_id: ldapId, username: 'alice', password: 'secret' })
		);
	});
});
