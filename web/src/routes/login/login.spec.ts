import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import LoginPage from './+page.svelte';

describe('login page', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/login');
		server.use(http.get('/auth/oidc/providers', ({ response }) => response(200).json([])));
	});

	it('clears a stale token instead of bouncing to /sql', async () => {
		localStorage.setItem('token', 'stale-token-from-another-deploy');
		localStorage.setItem('refresh_token', 'stale-refresh');
		server.use(
			http.get('/auth/me', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'expired session' })
			),
			http.post('/auth/refresh', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'expired session' })
			)
		);

		render(LoginPage);

		await waitFor(() => expect(localStorage.getItem('token')).toBeNull());
		// The form is still there: the page did not navigate away.
		expect(
			await screen.findByText('Welcome back. Enter your credentials to continue.')
		).toBeTruthy();
		expect(screen.getByPlaceholderText('you@company.com')).toBeTruthy();
	});

	it('renders the password field with a show/hide toggle', async () => {
		render(LoginPage);

		expect(await screen.findByRole('button', { name: 'Show password' })).toBeTruthy();
	});
});
