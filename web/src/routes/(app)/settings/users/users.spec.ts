import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import UsersPage from './+page.svelte';

const member = {
	id: '00000000-0000-0000-0000-0000000000c1',
	name: 'Grace Hopper',
	email: 'grace@example.test',
	role: 'member' as const,
	auth_provider: 'local' as const,
	status: 'active' as const,
	created_at: '2026-01-01T00:00:00Z'
};
const suspended = {
	id: '00000000-0000-0000-0000-0000000000c2',
	name: 'Alan Turing',
	email: 'alan@example.test',
	role: 'admin' as const,
	auth_provider: 'oidc' as const,
	status: 'suspended' as const,
	created_at: '2026-01-01T00:00:00Z'
};

function baseHandlers() {
	server.use(
		http.get('/users', ({ response }) =>
			response(200).json({
				data: [member, suspended],
				pagination: { page: 1, per_page: 200, total: 2, total_pages: 1 }
			})
		)
	);
}

describe('users settings', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/users');
	});

	it('lists users with role, status and provider', async () => {
		baseHandlers();
		renderPage(UsersPage, { me: makeMe(), url: '/settings/users' });

		expect(await screen.findByText('Grace Hopper')).toBeTruthy();
		expect(screen.getByText('Alan Turing')).toBeTruthy();
		expect(screen.getByText('active')).toBeTruthy();
		expect(screen.getByText('suspended')).toBeTruthy();
		expect(screen.getByText('oidc')).toBeTruthy();
	});

	it('filters by search', async () => {
		baseHandlers();
		renderPage(UsersPage, { me: makeMe(), url: '/settings/users' });
		await screen.findByText('Grace Hopper');

		await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'alan' } });
		expect(screen.queryByText('Grace Hopper')).toBeNull();
		expect(screen.getByText('Alan Turing')).toBeTruthy();
	});

	it('suspends after confirmation and sends the reason', async () => {
		baseHandlers();
		let body: unknown = null;
		server.use(
			http.post('/users/{id}/suspend', async ({ request, response }) => {
				body = await request.json();
				return response(200).json(member);
			})
		);
		renderPage(UsersPage, { me: makeMe(), url: '/settings/users' });
		await screen.findByText('Grace Hopper');

		await fireEvent.click(screen.getByRole('button', { name: 'Suspend' }));
		expect(await screen.findByText(/signed out everywhere/)).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Suspend user' }));

		await waitFor(() => expect(body).toEqual({ reason: 'admin' }));
	});

	it('changes a role after confirmation', async () => {
		baseHandlers();
		let body: unknown = null;
		server.use(
			http.patch('/users/{id}', async ({ request, response }) => {
				body = await request.json();
				return response(200).json({ ...member, role: 'admin' });
			})
		);
		renderPage(UsersPage, { me: makeMe(), url: '/settings/users' });
		await screen.findByText('Grace Hopper');

		await fireEvent.click(screen.getByRole('button', { name: 'Make admin' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Change role' }));

		await waitFor(() => expect(body).toEqual({ role: 'admin' }));
	});
});
