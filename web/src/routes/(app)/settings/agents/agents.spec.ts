import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import type { components } from '$lib/api/schema';
import { http, server } from '../../../../test/server';
import { makeMe, makeWorkspace } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import AgentsPage from './+page.svelte';

type Agent = components['schemas']['Agent'];

const agent: Agent = {
	id: '00000000-0000-0000-0000-0000000000a1',
	owner_id: '00000000-0000-0000-0000-000000000003',
	name: 'etl-pipeline',
	description: 'nightly loads',
	status: 'active',
	workspaces: [{ id: '00000000-0000-0000-0000-000000000001', name: 'Personal' }],
	active_token_count: 1,
	next_token_expiry: null,
	last_used_at: null,
	created_at: '2026-01-01T00:00:00Z',
	allowed_actions: ['view', 'update', 'disable', 'delete', 'manage_tokens', 'manage_policy']
};

describe('agents settings', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/agents');
	});

	it('lists agents and creates one with a workspace', async () => {
		let created: Record<string, unknown> | null = null;
		server.use(
			http.get('/agents', ({ response }) => response(200).json([agent])),
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()])),
			http.post('/agents', async ({ request, response }) => {
				created = (await request.json()) as Record<string, unknown>;
				return response(201).json({ ...agent, name: 'new-agent' });
			})
		);
		renderPage(AgentsPage, { me: makeMe(), url: '/settings/agents' });

		expect(await screen.findByText('etl-pipeline')).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'New agent' }));
		await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'new-agent' } });
		await fireEvent.click(screen.getByLabelText('Personal'));
		await fireEvent.click(screen.getByRole('button', { name: 'Create agent' }));

		await waitFor(() =>
			expect(created).toMatchObject({
				name: 'new-agent',
				workspace_ids: [makeWorkspace().id]
			})
		);
	});
});
