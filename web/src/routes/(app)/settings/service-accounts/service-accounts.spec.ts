import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeMe, makeWorkspace } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import ServiceAccountsPage from './+page.svelte';

const account = {
	id: '00000000-0000-0000-0000-0000000005a1',
	name: 'Reporting bot',
	email: 'reporting-bot-1a2b@service-accounts.baleia.invalid',
	description: null,
	status: 'active' as const,
	identity_provider_id: null,
	client_id: null,
	home_workspace_id: null,
	created_at: '2026-01-01T00:00:00Z',
	allowed_actions: ['view', 'update', 'delete', 'manage_tokens'] as (
		'view' | 'update' | 'delete' | 'manage_tokens'
	)[]
};

describe('Settings · Service accounts (#653)', () => {
	it('creates a service account and shows its new token once', async () => {
		let accounts: (typeof account)[] = [];
		const created: unknown[] = [];
		server.use(
			http.get('/service-accounts', ({ response }) => response(200).json(accounts)),
			http.get('/auth/providers', ({ response }) => response(200).json([])),
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()])),
			http.post('/service-accounts', async ({ request, response }) => {
				created.push(await request.json());
				accounts = [account];
				return response(201).json(account);
			}),
			http.get('/service-accounts/{id}/tokens', ({ response }) => response(200).json([])),
			http.post('/service-accounts/{id}/tokens', ({ response }) =>
				response(201).json({
					id: '00000000-0000-0000-0000-0000000005b1',
					name: 'ci',
					key_prefix: 'bal_abcd',
					expires_at: null,
					created_at: '2026-01-01T00:00:00Z',
					secret: 'bal_the-secret'
				})
			)
		);
		renderPage(ServiceAccountsPage, { me: makeMe() });

		expect(await screen.findByText('No service accounts')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'New service account' }));
		await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Reporting bot' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create' }));
		await waitFor(() => expect(created).toEqual([{ name: 'Reporting bot' }]));

		expect(await screen.findByRole('heading', { name: 'Reporting bot' })).toBeTruthy();
		await fireEvent.input(screen.getByLabelText('Token name'), { target: { value: 'ci' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Issue token' }));
		expect(await screen.findByText('bal_the-secret')).toBeTruthy();
	});
});
