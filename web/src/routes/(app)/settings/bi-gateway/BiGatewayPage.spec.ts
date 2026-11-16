import { fireEvent, screen, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeBIGatewayStatus, makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import BiGatewayPage from './+page.svelte';

describe('BI gateway administration page', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/bi-gateway');
		server.use(
			http.get('/admin/bi-gateway', ({ response }) => response(200).json(makeBIGatewayStatus()))
		);
	});

	it('shows a ready gateway with its public URL, health and limits', async () => {
		renderPage(BiGatewayPage, { me: makeMe(), url: '/settings/bi-gateway' });

		expect(await screen.findByText('Ready')).toBeTruthy();
		expect(screen.getByText('https://bi.example.com')).toBeTruthy();
		expect(screen.getByText('Operational')).toBeTruthy();
		expect(screen.getByText('5')).toBeTruthy();
		expect(screen.getByText('20')).toBeTruthy();
		expect(screen.getByText('Nothing is blocking the gateway.')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Disable gateway' })).toBeTruthy();
	});

	it('shows the disabled state and the switch that turns it on', async () => {
		server.use(
			http.get('/admin/bi-gateway', ({ response }) =>
				response(200).json(
					makeBIGatewayStatus({
						enabled: false,
						state: 'disabled',
						blocked_by: ['gateway_disabled']
					})
				)
			)
		);
		renderPage(BiGatewayPage, { me: makeMe(), url: '/settings/bi-gateway' });

		expect(await screen.findByText('Disabled')).toBeTruthy();
		expect(screen.getByText('The global switch is off. Enable the gateway below.')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Enable gateway' })).toBeTruthy();
	});

	it('names what is missing instead of claiming the gateway is ready', async () => {
		server.use(
			http.get('/admin/bi-gateway', ({ response }) =>
				response(200).json(
					makeBIGatewayStatus({
						configured: false,
						public_url_ready: false,
						public_url: null,
						state: 'blocked',
						blocked_by: ['public_url_not_configured']
					})
				)
			)
		);
		renderPage(BiGatewayPage, { me: makeMe(), url: '/settings/bi-gateway' });

		expect(await screen.findByText('Blocked')).toBeTruthy();
		expect(screen.queryByText('Ready')).toBeNull();
		expect(
			screen.getByText(
				'The public URL is not configured. Set `biGateway.publicURL` (or `BALEIA_BI_GATEWAY_URL`).'
			)
		).toBeTruthy();
	});

	it('renders the API error when the caller is not authorized', async () => {
		server.use(
			http.get('/admin/bi-gateway', ({ response }) =>
				response(403).json({ code: 'forbidden', message: 'administrator role required' })
			)
		);
		renderPage(BiGatewayPage, { me: makeMe(), url: '/settings/bi-gateway' });

		expect(await screen.findByText('You do not have permission to do this.')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
	});

	it('confirms before disabling and shows the backend status after', async () => {
		server.use(
			http.put('/admin/bi-gateway', ({ response }) =>
				response(200).json(
					makeBIGatewayStatus({
						enabled: false,
						state: 'disabled',
						blocked_by: ['gateway_disabled']
					})
				)
			)
		);
		renderPage(BiGatewayPage, { me: makeMe(), url: '/settings/bi-gateway' });

		await screen.findByText('Ready');
		await fireEvent.click(screen.getByRole('button', { name: 'Disable gateway' }));
		const dialog = await screen.findByRole('dialog');
		expect(within(dialog).getByText(/rejected immediately/)).toBeTruthy();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Disable gateway' }));

		expect(await screen.findByText('Disabled')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Enable gateway' })).toBeTruthy();
	});

	// A refresh started before the switch must not restore the old snapshot
	// when it resolves after the confirmed change.
	it('ignores a refresh that resolves after a confirmed switch', async () => {
		renderPage(BiGatewayPage, { me: makeMe(), url: '/settings/bi-gateway' });
		await screen.findByText('Ready');

		let release: () => void = () => {};
		const gate = new Promise<void>((resolve) => (release = resolve));
		server.use(
			http.get('/admin/bi-gateway', async ({ response }) => {
				await gate;
				// The stale snapshot still says ready and enabled.
				return response(200).json(makeBIGatewayStatus());
			}),
			http.put('/admin/bi-gateway', ({ response }) =>
				response(200).json(
					makeBIGatewayStatus({
						enabled: false,
						state: 'disabled',
						blocked_by: ['gateway_disabled']
					})
				)
			)
		);

		await fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Disable gateway' }));
		const dialog = await screen.findByRole('dialog');
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Disable gateway' }));
		expect(await screen.findByText('Disabled')).toBeTruthy();

		release();
		await vi.waitFor(() => expect(screen.queryByText('Ready')).toBeNull());
	});
});
