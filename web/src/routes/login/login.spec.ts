import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import LoginPage from './+page.svelte';

describe('login page', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/login');
		server.use(
			http.get('/auth/providers', ({ response }) => response(200).json([])),
			http.get('/auth/me', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'no session' })
			),
			http.post('/auth/refresh', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'no session' })
			)
		);
	});

	it('keeps the form usable when there is no session and persists no tokens', async () => {
		render(LoginPage);

		// The form is still there: the page did not navigate away.
		expect(
			await screen.findByText('Welcome back. Enter your credentials to continue.')
		).toBeTruthy();
		expect(screen.getByPlaceholderText('you@company.com')).toBeTruthy();
		await waitFor(() => expect(localStorage.getItem('token')).toBeNull());
		expect(localStorage.getItem('refresh_token')).toBeNull();
	});

	it('shows the released version from the release-please manifest', async () => {
		render(LoginPage);

		// Injected at build time (#513); never a hard-coded tag.
		expect(await screen.findByText(`v${__APP_VERSION__}`)).toBeTruthy();
		expect(__APP_VERSION__).toMatch(/^\d+\.\d+\.\d+/);
		expect(screen.queryByText('v1.0.0-beta')).toBeNull();
	});

	it('renders the password field with a show/hide toggle', async () => {
		render(LoginPage);

		expect(await screen.findByRole('button', { name: 'Show password' })).toBeTruthy();
	});
});
