import { fireEvent, screen, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../../../../test/server';
import {
	makeBiKey,
	makeMe,
	makeWorkspace,
	makeWorkspaceBIStatus,
	makeWorkspaceTarget
} from '../../../../../test/factories';
import { renderPage } from '../../../../../test/render';
import WorkspacePage from './+page.svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({
	page: {
		url: new URL(
			'http://localhost/settings/workspaces/00000000-0000-0000-0000-000000000001?tab=bi'
		),
		params: { workspace_id: '00000000-0000-0000-0000-000000000001' }
	}
}));

const url = '/settings/workspaces/00000000-0000-0000-0000-000000000001?tab=bi';

function serveBase(workspace = makeWorkspace()) {
	server.use(
		http.get('/workspaces', ({ response }) => response(200).json([workspace])),
		http.get('/workspaces/{id}/targets', ({ response }) =>
			response(200).json([makeWorkspaceTarget()])
		),
		http.get('/workspaces/{id}/catalog-access', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/impact', ({ response }) =>
			response(200).json({
				saved_queries: 0,
				metrics: 0,
				spaces: 0,
				running_executions: 0,
				default_target_affected: false
			})
		),
		http.get('/catalog-grants', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/bi-status', ({ response }) =>
			response(200).json(makeWorkspaceBIStatus())
		),
		http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json([makeBiKey()]))
	);
}

describe('workspace BI access (#1100)', () => {
	beforeEach(() => {
		localStorage.clear();
		serveBase();
	});

	it('lists the workspace keys and the actions of a workspace admin', async () => {
		renderPage(WorkspacePage, { me: makeMe(), url });

		expect(await screen.findByText('Tableau')).toBeTruthy();
		expect(screen.getByText('BI keys')).toBeTruthy();
		expect(screen.getByText('ada@example.com')).toBeTruthy();
		expect(screen.getByText('trino')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Create key' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Disable BI' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Revoke' })).toBeTruthy();
	});

	it('shows the empty state before the first key', async () => {
		server.use(http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json([])));
		renderPage(WorkspacePage, { me: makeMe(), url });

		expect(await screen.findByText('No BI keys yet.')).toBeTruthy();
	});

	it('creates a key and shows its secret once', async () => {
		server.use(
			http.post('/workspaces/{id}/bi-keys', ({ response }) =>
				response(201).json({ ...makeBiKey({ name: 'Power BI' }), secret: 'bal_secret_123' })
			)
		);
		renderPage(WorkspacePage, { me: makeMe(), url });

		await screen.findByText('BI keys');
		await fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
		const dialog = await screen.findByRole('dialog', { name: 'Create a BI key' });
		await fireEvent.input(within(dialog).getByLabelText('Name'), {
			target: { value: 'Power BI' }
		});
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }));

		const secretDialog = await screen.findByRole('dialog', { name: 'Copy this BI key now' });
		expect(within(secretDialog).getByText('bal_secret_123')).toBeTruthy();
		expect(window.location.href).not.toContain('bal_secret_123');

		await fireEvent.click(within(secretDialog).getByLabelText('I have copied this value'));
		await fireEvent.click(within(secretDialog).getByRole('button', { name: 'Done' }));
		expect(screen.queryByText('bal_secret_123')).toBeNull();
	});

	it('keeps the create form open and names the error when creation fails', async () => {
		server.use(
			http.post('/workspaces/{id}/bi-keys', ({ response }) =>
				response(422).json({
					code: 'validation_error',
					message: 'engine_id must be an enabled Trino target of the workspace',
					errors: []
				})
			)
		);
		renderPage(WorkspacePage, { me: makeMe(), url });

		await screen.findByText('BI keys');
		await fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
		const dialog = await screen.findByRole('dialog', { name: 'Create a BI key' });
		await fireEvent.input(within(dialog).getByLabelText('Name'), {
			target: { value: 'Power BI' }
		});
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }));

		expect(await within(dialog).findByText('Please check the highlighted fields.')).toBeTruthy();
		expect(screen.getByRole('dialog', { name: 'Create a BI key' })).toBeTruthy();
	});

	it('revokes a key after confirmation', async () => {
		let keys = [makeBiKey()];
		server.use(
			http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json(keys)),
			http.delete('/workspaces/{id}/bi-keys/{key_id}', ({ response }) => {
				keys = [];
				return response(204).empty();
			})
		);
		renderPage(WorkspacePage, { me: makeMe(), url });

		await fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
		const dialog = await screen.findByRole('dialog', { name: 'Revoke the BI key' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));

		expect(await screen.findByText('No BI keys yet.')).toBeTruthy();
	});

	it('shows the revoke failure inside the confirmation', async () => {
		server.use(
			http.delete('/workspaces/{id}/bi-keys/{key_id}', ({ response }) =>
				response(403).json({ code: 'forbidden', message: 'not allowed' })
			)
		);
		renderPage(WorkspacePage, { me: makeMe(), url });

		await fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
		const dialog = await screen.findByRole('dialog', { name: 'Revoke the BI key' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));

		expect(await within(dialog).findByText('You do not have permission to do this.')).toBeTruthy();
	});

	it('shows the switch as read-only and only the caller keys to a member', async () => {
		server.use(
			http.get('/workspaces', ({ response }) =>
				response(200).json([makeWorkspace({ allowed_actions: ['view'] })])
			),
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(makeWorkspaceBIStatus({ can_manage_keys: false }))
			)
		);
		renderPage(WorkspacePage, { me: makeMe(), url });

		expect(await screen.findByText('A workspace admin manages this switch.')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Disable BI' })).toBeNull();
		expect(screen.getByRole('button', { name: 'Create key' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Revoke' })).toBeTruthy();
	});

	it('names the blocker when the workspace BI switch is off', async () => {
		server.use(
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(
					makeWorkspaceBIStatus({
						workspace_bi_enabled: false,
						state: 'blocked',
						blocked_by: ['workspace_bi_disabled']
					})
				)
			)
		);
		renderPage(WorkspacePage, { me: makeMe(), url });

		expect(
			await screen.findByText('The workspace BI switch is off; its keys do not work.')
		).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Enable BI' })).toBeTruthy();
	});

	it('hides key creation when no Trino target exists', async () => {
		server.use(
			http.get('/workspaces/{id}/targets', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(
					makeWorkspaceBIStatus({
						can_create_keys: false,
						trino_targets: { available: false, count: 0 },
						state: 'blocked',
						blocked_by: ['no_trino_targets']
					})
				)
			)
		);
		renderPage(WorkspacePage, { me: makeMe(), url });

		expect(
			await screen.findByText('This workspace has no Trino target to point a key at.')
		).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Create key' })).toBeNull();
	});

	it('marks an expired key', async () => {
		server.use(
			http.get('/workspaces/{id}/bi-keys', ({ response }) =>
				response(200).json([makeBiKey({ expires_at: '2020-01-01T00:00:00Z' })])
			)
		);
		renderPage(WorkspacePage, { me: makeMe(), url });

		expect(await screen.findByText('Expired')).toBeTruthy();
	});
});
