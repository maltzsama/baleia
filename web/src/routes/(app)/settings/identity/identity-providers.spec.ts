import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeMe, makeWorkspace } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import IdentityPage from './+page.svelte';

const oidc = {
	id: '00000000-0000-0000-0000-0000000000b1',
	name: 'Okta',
	type: 'oidc' as const,
	enabled: true,
	has_client_secret: true,
	has_bind_password: false,
	linked_users: 2,
	created_at: '2026-01-01T00:00:00Z',
	updated_at: '2026-01-01T00:00:00Z'
};
const ldap = {
	id: '00000000-0000-0000-0000-0000000000b2',
	name: 'Corp LDAP',
	type: 'ldap' as const,
	enabled: false,
	has_client_secret: false,
	has_bind_password: true,
	linked_users: 0,
	created_at: '2026-01-01T00:00:00Z'
};

function baseHandlers() {
	server.use(
		http.get('/admin/identity-providers', ({ response }) => response(200).json([oidc, ldap])),
		http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()]))
	);
}

describe('identity providers settings', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/identity');
	});

	it('lists providers with type, status and linked users', async () => {
		baseHandlers();
		renderPage(IdentityPage, { me: makeMe(), url: '/settings/identity' });

		expect(await screen.findByText('Okta')).toBeTruthy();
		expect(screen.getByText('Corp LDAP')).toBeTruthy();
		expect(screen.getByText('OIDC')).toBeTruthy();
		expect(screen.getByText('LDAP')).toBeTruthy();
		expect(screen.getByText(/2 linked users/)).toBeTruthy();
		expect(screen.getByText('Enabled')).toBeTruthy();
		expect(screen.getByText('Disabled')).toBeTruthy();
	});

	it('deletes with consequences and the suspend_users flag', async () => {
		baseHandlers();
		let deleted = false;
		let suspend = '';
		server.use(
			http.delete('/admin/identity-providers/{id}', ({ request, response }) => {
				deleted = true;
				suspend = new URL(request.url).searchParams.get('suspend_users') ?? '';
				return response(204).empty();
			})
		);

		renderPage(IdentityPage, { me: makeMe(), url: '/settings/identity' });
		await screen.findByText('Okta');

		await fireEvent.click(screen.getAllByRole('button', { name: 'Delete' })[0]);
		expect(await screen.findByText(/Users linked to this provider are suspended/)).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Delete provider' }));

		await waitFor(() => expect(deleted).toBe(true));
		expect(suspend).toBe('true');
	});

	it('creates an OIDC provider from the form', async () => {
		baseHandlers();
		let body: Record<string, unknown> | null = null;
		server.use(
			http.post('/admin/identity-providers', async ({ request, response }) => {
				body = (await request.json()) as Record<string, unknown>;
				return response(201).json(oidc);
			})
		);

		renderPage(IdentityPage, { me: makeMe(), url: '/settings/identity' });
		await screen.findByText('Okta');
		await fireEvent.click(screen.getByRole('button', { name: 'Add provider' }));

		await fireEvent.input(await screen.findByLabelText('Name'), { target: { value: 'New IdP' } });
		await fireEvent.input(screen.getByLabelText('Issuer URL'), {
			target: { value: 'https://idp.example.test' }
		});
		await fireEvent.input(screen.getByLabelText('Client ID'), { target: { value: 'baleia' } });
		await fireEvent.input(screen.getByLabelText('Client secret'), { target: { value: 'secret' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create provider' }));

		await waitFor(() => expect(body).not.toBeNull());
		expect(body).toMatchObject({
			name: 'New IdP',
			type: 'oidc',
			enabled: true,
			oidc: { issuer_url: 'https://idp.example.test', client_id: 'baleia' }
		});
	});
});
