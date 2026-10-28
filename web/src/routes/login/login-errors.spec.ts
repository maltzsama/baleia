import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import LoginPage from './+page.svelte';

function baseHandlers() {
	server.use(
		http.get('/auth/providers', ({ response }) => response(200).json([])),
		http.get('/auth/me', ({ response }) =>
			response(401).json({ code: 'unauthorized', message: 'no session' })
		),
		http.post('/auth/refresh', ({ response }) =>
			response(401).json({ code: 'unauthorized', message: 'no session' })
		)
	);
}

async function submit(email: string, password: string) {
	await fireEvent.input(screen.getByPlaceholderText('you@company.com'), {
		target: { value: email }
	});
	await fireEvent.input(screen.getByLabelText('Password'), { target: { value: password } });
	await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
}

describe('login errors by code', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/login');
	});

	it('renders an SSO code from the query and removes it from the address bar', async () => {
		window.history.replaceState(null, '', '/login?error=account_suspended&next=/sql');
		baseHandlers();

		render(LoginPage);

		expect(await screen.findByText(/Account suspended/)).toBeTruthy();
		await waitFor(() => expect(window.location.search).toBe('?next=%2Fsql'));
	});

	it('renders invalid_credentials from the catalog', async () => {
		baseHandlers();
		server.use(
			http.post('/auth/login', ({ response }) =>
				response(401).json({ code: 'invalid_credentials', message: 'nope' })
			)
		);

		render(LoginPage);
		await screen.findByText('Welcome back. Enter your credentials to continue.');
		await submit('ada@example.com', 'wrong');

		expect(await screen.findByText(/Invalid credentials/)).toBeTruthy();
	});

	it('counts down from Retry-After on 429', async () => {
		baseHandlers();
		server.use(
			http.post('/auth/login', ({ response }) =>
				response(429).json(
					{ code: 'rate_limited', message: 'slow down' },
					{ headers: { 'Retry-After': '30' } }
				)
			)
		);

		render(LoginPage);
		await screen.findByText('Welcome back. Enter your credentials to continue.');
		await submit('ada@example.com', 'secret');

		expect(await screen.findByText(/Too many requests\. Try again in 30 s\./)).toBeTruthy();
	});

	it('says it is still checking after one second', async () => {
		baseHandlers();
		server.use(
			http.post('/auth/login', async ({ response }) => {
				await new Promise((resolve) => setTimeout(resolve, 1200));
				return response(401).json({ code: 'invalid_credentials', message: 'nope' });
			})
		);

		render(LoginPage);
		await screen.findByText('Welcome back. Enter your credentials to continue.');
		await submit('ada@example.com', 'secret');

		expect(
			await screen.findByRole('button', { name: 'Checking credentials…' }, { timeout: 2500 })
		).toBeTruthy();
	});
	// #484: the theme switch is a named switch; errors are announced.
	it('names the theme switch and toggles its state', async () => {
		baseHandlers();
		render(LoginPage);
		const toggle = screen.getByRole('switch', { name: 'Dark theme' });
		expect(toggle.tagName).toBe('BUTTON');
		const before = toggle.getAttribute('aria-checked');
		await fireEvent.click(toggle);
		expect(toggle.getAttribute('aria-checked')).not.toBe(before);
		await fireEvent.click(toggle);
	});

	it('announces a sign-in error as an alert', async () => {
		baseHandlers();
		server.use(
			http.post('/auth/login', ({ response }) =>
				response(401).json({ code: 'invalid_credentials', message: 'bad' })
			)
		);
		render(LoginPage);
		await submit('ada@example.com', 'wrong-password');
		expect(await screen.findByRole('alert')).toBeTruthy();
	});
});
