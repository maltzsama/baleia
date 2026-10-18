import { fireEvent, render, screen } from '@testing-library/svelte';
import { HttpResponse } from 'msw';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import LoginPage from './+page.svelte';

describe('forgot-password flow', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/login');
		server.use(
			http.get('/auth/oidc/providers', ({ response }) => response(200).json([])),
			http.get('/auth/me', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'no session' })
			),
			http.post('/auth/refresh', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'no session' })
			)
		);
	});

	async function submitReset() {
		render(LoginPage);
		await screen.findByText('Welcome back. Enter your credentials to continue.');
		await fireEvent.click(screen.getByText('Forgot password?'));
		await fireEvent.input(screen.getByPlaceholderText('you@company.com'), {
			target: { value: 'user@example.com' }
		});
		await fireEvent.click(screen.getByRole('button', { name: /Send reset link/ }));
	}

	it('shows the same answer for every account on 204', async () => {
		server.use(http.post('/auth/password-reset/request', ({ response }) => response(204).empty()));

		await submitReset();

		expect(
			await screen.findByText(/If an account exists for this address, we sent a link/)
		).toBeTruthy();
		expect(screen.getByText(/expires in 30 minutes/)).toBeTruthy();
	});

	it('explains a server without e-mail on 503', async () => {
		server.use(
			http.post('/auth/password-reset/request', ({ response }) =>
				response(503).json({ code: 'service_unavailable', message: 'email service not configured' })
			)
		);

		await submitReset();

		expect(await screen.findByText(/not configured on this server/)).toBeTruthy();
	});

	it('shows a network error instead of swallowing it', async () => {
		server.use(http.post('/auth/password-reset/request', () => HttpResponse.error()));

		await submitReset();

		expect(await screen.findByText(/Connection error/)).toBeTruthy();
	});
});
