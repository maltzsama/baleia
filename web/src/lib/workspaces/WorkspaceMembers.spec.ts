import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { makeWorkspaceMember } from '../../test/factories';
import WorkspaceMembers from './WorkspaceMembers.svelte';

const WS = '00000000-0000-0000-0000-000000000001';

const owner = makeWorkspaceMember({
	user: {
		...makeWorkspaceMember().user,
		id: '00000000-0000-0000-0000-0000000000a0',
		name: 'Ada Lovelace',
		email: 'owner@example.com'
	},
	role: 'admin',
	source: 'owner'
});

describe('WorkspaceMembers (#653)', () => {
	it('lets an admin change a role and share a personal workspace by e-mail', async () => {
		let members = [owner];
		const puts: unknown[] = [];
		const posts: unknown[] = [];
		server.use(
			http.get('/workspaces/{id}/members', ({ response }) => response(200).json(members)),
			http.put('/workspaces/{id}/members/{user_id}', async ({ request, response }) => {
				puts.push(await request.json());
				return response(200).json(members[1]);
			}),
			http.post('/workspaces/{id}/members', async ({ request, response }) => {
				const body = (await request.json()) as { email: string; role: 'viewer' };
				posts.push(body);
				const shared = makeWorkspaceMember({ source: 'shared', role: body.role, role_set: true });
				members = [owner, shared];
				return response(200).json(shared);
			})
		);
		render(WorkspaceMembers, { props: { workspaceId: WS, personal: true, canManage: true } });

		expect(await screen.findByText('Ada Lovelace')).toBeTruthy();
		await fireEvent.input(screen.getByLabelText('Share with (e-mail)'), {
			target: { value: 'grace@example.com' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Share' }));
		await waitFor(() => expect(posts).toEqual([{ email: 'grace@example.com', role: 'viewer' }]));
		expect(await screen.findByText('Grace Hopper')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Stop sharing' })).toBeTruthy();

		await fireEvent.change(screen.getByLabelText('Role of Grace Hopper'), {
			target: { value: 'editor' }
		});
		await waitFor(() => expect(puts).toEqual([{ role: 'editor' }]));
	});

	it('shows roles read-only to a non-admin', async () => {
		server.use(
			http.get('/workspaces/{id}/members', ({ response }) =>
				response(200).json([owner, makeWorkspaceMember()])
			)
		);
		render(WorkspaceMembers, { props: { workspaceId: WS, personal: false, canManage: false } });

		expect(await screen.findByText('Grace Hopper')).toBeTruthy();
		expect(screen.getByText('Editor')).toBeTruthy();
		expect(screen.queryByLabelText('Role of Grace Hopper')).toBeNull();
		expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
	});
});
