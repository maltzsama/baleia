import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import TeamsPage from './+page.svelte';

const team = {
	id: '00000000-0000-0000-0000-0000000000d1',
	name: 'Engineering',
	description: 'Platform team',
	created_at: '2026-01-01T00:00:00Z'
};
const other = {
	id: '00000000-0000-0000-0000-0000000000d2',
	name: 'Analytics',
	description: null,
	created_at: '2026-01-01T00:00:00Z'
};
const alice = {
	id: '00000000-0000-0000-0000-0000000000c1',
	name: 'Alice',
	email: 'alice@example.test',
	role: 'member' as const,
	auth_provider: 'local' as const,
	status: 'active' as const,
	created_at: '2026-01-01T00:00:00Z'
};
const bob = {
	id: '00000000-0000-0000-0000-0000000000c2',
	name: 'Bob',
	email: 'bob@example.test',
	role: 'member' as const,
	auth_provider: 'local' as const,
	status: 'active' as const,
	created_at: '2026-01-01T00:00:00Z'
};

function baseHandlers() {
	server.use(
		http.get('/teams', ({ response }) => response(200).json([team, other])),
		http.get('/teams/{id}/members', ({ params, response }) =>
			response(200).json(
				params.id === team.id
					? [
							{ user: alice, role: 'owner', joined_at: '2026-01-01T00:00:00Z' },
							{ user: bob, role: 'member', joined_at: '2026-01-01T00:00:00Z' }
						]
					: []
			)
		)
	);
}

describe('teams settings', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/teams');
	});

	it('shows a member only the teams the server scopes to them', async () => {
		baseHandlers();
		// The server scopes /teams for a member; the page renders that answer.
		server.use(http.get('/teams', ({ response }) => response(200).json([team])));
		renderPage(TeamsPage, {
			me: makeMe({ role: 'member', global_actions: [] }),
			url: '/settings/teams'
		});

		expect(await screen.findByText('Engineering')).toBeTruthy();
		expect(screen.queryByText('Analytics')).toBeNull();
		expect(screen.getByText('Alice')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'New team' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Add member' })).toBeNull();
	});

	it('lets an admin manage membership and roles', async () => {
		baseHandlers();
		renderPage(TeamsPage, { me: makeMe(), url: '/settings/teams' });

		expect(await screen.findByRole('button', { name: 'New team' })).toBeTruthy();
		await screen.findByText('Engineering');
		expect(screen.getAllByRole('button', { name: 'Add member' }).length).toBe(2);
	});

	it('changes a team role after confirmation', async () => {
		baseHandlers();
		let body: unknown = null;
		server.use(
			http.patch('/teams/{id}/members/{user_id}', async ({ request, response }) => {
				body = await request.json();
				return response(200).json({ user: bob, role: 'owner', joined_at: '2026-01-01T00:00:00Z' });
			})
		);
		renderPage(TeamsPage, { me: makeMe(), url: '/settings/teams' });
		await screen.findByText('Engineering');

		await fireEvent.click(screen.getByRole('button', { name: 'Make owner' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Change role' }));

		await waitFor(() => expect(body).toEqual({ role: 'owner' }));
	});

	it('removes a member after confirmation', async () => {
		baseHandlers();
		let removed = false;
		server.use(
			http.delete('/teams/{id}/members/{user_id}', ({ response }) => {
				removed = true;
				return response(204).empty();
			})
		);
		renderPage(TeamsPage, { me: makeMe(), url: '/settings/teams' });
		await screen.findByText('Engineering');

		await fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0]);
		await fireEvent.click(await screen.findByRole('button', { name: 'Remove member' }));

		await waitFor(() => expect(removed).toBe(true));
	});
});
