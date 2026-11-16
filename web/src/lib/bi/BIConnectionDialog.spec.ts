import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../test/server';
import { makeBiKey, makeWorkspaceBIStatus } from '../../test/factories';
import BIConnectionDialog from './BIConnectionDialog.svelte';

const props = {
	open: true,
	tool: 'tableau' as const,
	workspaceId: '00000000-0000-0000-0000-000000000001',
	workspaceName: 'Analytics',
	engineId: '00000000-0000-0000-0000-000000000004',
	engineName: 'trino',
	catalog: 'lake_t',
	schema: 'sales',
	table: 'orders'
};

describe('BI connection dialog (#1067)', () => {
	beforeEach(() => {
		Object.defineProperty(URL, 'createObjectURL', {
			value: vi.fn(() => 'blob:test'),
			configurable: true
		});
		Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn(), configurable: true });
		vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
	});

	it('summarizes the target and downloads the file for a selected key', async () => {
		let requested = '';
		const saved: Blob[] = [];
		Object.defineProperty(URL, 'createObjectURL', {
			value: vi.fn((blob: Blob) => {
				saved.push(blob);
				return 'blob:test';
			}),
			configurable: true
		});
		server.use(
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(makeWorkspaceBIStatus())
			),
			http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json([makeBiKey()])),
			http.get('/workspaces/{id}/bi/{tool}', ({ request, response }) => {
				requested = request.url;
				return response(200).text('<datasource />');
			})
		);
		render(BIConnectionDialog, { props });

		expect(await screen.findByText('Analytics')).toBeTruthy();
		expect(screen.getByText('trino')).toBeTruthy();
		expect(screen.getAllByText('lake_t').length).toBeGreaterThan(0);
		expect(screen.getAllByText('sales').length).toBeGreaterThan(0);

		await fireEvent.click(screen.getByRole('button', { name: 'Download connection file' }));
		await waitFor(() => expect(requested).toContain('/bi/tableau'));
		const params = new URL(requested).searchParams;
		expect(params.get('engine_id')).toBe(props.engineId);
		expect(params.get('catalog')).toBe('lake_t');
		expect(params.get('schema')).toBe('sales');
		expect(params.get('table')).toBe('orders');
		await waitFor(() => expect(saved.length).toBe(1));
		expect(await saved[0].text()).toBe('<datasource />');
	});

	it('explains a blocked gateway and links the remediation', async () => {
		server.use(
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(
					makeWorkspaceBIStatus({
						workspace_bi_enabled: false,
						state: 'blocked',
						blocked_by: ['workspace_bi_disabled']
					})
				)
			),
			http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json([]))
		);
		render(BIConnectionDialog, { props });

		expect(await screen.findByText('This connection is not ready yet.')).toBeTruthy();
		expect(screen.getByText('The workspace BI switch is off; its keys do not work.')).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Open the workspace BI access' })).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Open the BI gateway settings' })).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Deployment guide' })).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Download connection file' })).toBeNull();
	});

	it('creates a key and shows the secret once', async () => {
		server.use(
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(makeWorkspaceBIStatus({ can_create_keys: true }))
			),
			http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json([])),
			http.post('/workspaces/{id}/bi-keys', ({ response }) =>
				response(201).json({ ...makeBiKey({ name: 'Probe' }), secret: 'bal_secret_9' })
			)
		);
		render(BIConnectionDialog, { props });

		expect(await screen.findByText('You have no key for this engine yet.')).toBeTruthy();
		const download = screen.getByRole('button', {
			name: 'Download connection file'
		}) as HTMLButtonElement;
		expect(download.disabled).toBe(true);

		await fireEvent.input(screen.getByLabelText('Key name'), { target: { value: 'Probe' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create a key' }));

		const secretDialog = await screen.findByRole('dialog', { name: 'Copy this BI key now' });
		expect(within(secretDialog).getByText('bal_secret_9')).toBeTruthy();
		expect(window.location.href).not.toContain('bal_secret_9');

		await fireEvent.click(within(secretDialog).getByLabelText('I have copied this value'));
		await fireEvent.click(within(secretDialog).getByRole('button', { name: 'Done' }));
		expect(screen.queryByText('bal_secret_9')).toBeNull();
		await waitFor(() =>
			expect(
				(screen.getByRole('button', { name: 'Download connection file' }) as HTMLButtonElement)
					.disabled
			).toBe(false)
		);
	});

	it('keeps the create error inside the dialog', async () => {
		server.use(
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(makeWorkspaceBIStatus({ can_create_keys: true }))
			),
			http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json([])),
			http.post('/workspaces/{id}/bi-keys', ({ response }) =>
				response(422).json({
					code: 'validation_error',
					message: 'engine_id must be an enabled Trino target of the workspace',
					errors: []
				})
			)
		);
		render(BIConnectionDialog, { props });

		await screen.findByText('You have no key for this engine yet.');
		await fireEvent.input(screen.getByLabelText('Key name'), { target: { value: 'Probe' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create a key' }));

		expect(await screen.findByText('Please check the highlighted fields.')).toBeTruthy();
		expect(screen.getByRole('dialog', { name: 'Download the Tableau connection' })).toBeTruthy();
	});

	it('reports a download failure without closing the dialog', async () => {
		server.use(
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(makeWorkspaceBIStatus())
			),
			http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json([makeBiKey()])),
			http.get('/workspaces/{id}/bi/{tool}', ({ response }) =>
				response(422).json({
					code: 'validation_error',
					message: 'the BI gateway is not enabled',
					errors: []
				})
			)
		);
		render(BIConnectionDialog, { props });

		await fireEvent.click(await screen.findByRole('button', { name: 'Download connection file' }));

		expect(await screen.findByText('Please check the highlighted fields.')).toBeTruthy();
	});

	it('does not count an expired key as usable', async () => {
		server.use(
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(makeWorkspaceBIStatus())
			),
			http.get('/workspaces/{id}/bi-keys', ({ response }) =>
				response(200).json([makeBiKey({ expires_at: '2020-01-01T00:00:00Z' })])
			)
		);
		render(BIConnectionDialog, { props });

		expect(await screen.findByText('You have no key for this engine yet.')).toBeTruthy();
		const download = screen.getByRole('button', {
			name: 'Download connection file'
		}) as HTMLButtonElement;
		expect(download.disabled).toBe(true);
	});
});
