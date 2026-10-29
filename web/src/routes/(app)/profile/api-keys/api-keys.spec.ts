import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import ApiKeysPage from './+page.svelte';

const key = {
	id: '00000000-0000-0000-0000-0000000000e1',
	name: 'ci-pipeline',
	key_prefix: 'bal_abc',
	expires_at: null,
	created_at: '2026-01-01T00:00:00Z'
};

describe('api keys settings', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/profile/api-keys');
	});

	it('creates a key and shows the secret exactly once', async () => {
		server.use(
			http.get('/api-keys', ({ response }) => response(200).json([key])),
			http.post('/api-keys', ({ response }) =>
				response(201).json({ ...key, name: 'agent', secret: 'bal_secret_once' })
			)
		);
		renderPage(ApiKeysPage, { me: makeMe(), url: '/profile/api-keys' });
		await screen.findByText('ci-pipeline');

		await fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
		await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'agent' } });
		const createButtons = screen.getAllByRole('button', { name: 'Create key' });
		await fireEvent.click(createButtons[createButtons.length - 1]);

		expect(await screen.findByText('bal_secret_once')).toBeTruthy();

		await fireEvent.click(screen.getByLabelText('I have copied this value'));
		await fireEvent.click(screen.getByRole('button', { name: 'Done' }));
		await waitFor(() => expect(screen.queryByText('bal_secret_once')).toBeNull());
	});

	it('revokes a key after confirmation', async () => {
		let revoked = false;
		server.use(
			http.get('/api-keys', ({ response }) => response(200).json([key])),
			http.delete('/api-keys/{id}', ({ response }) => {
				revoked = true;
				return response(204).empty();
			})
		);
		renderPage(ApiKeysPage, { me: makeMe(), url: '/profile/api-keys' });
		await screen.findByText('ci-pipeline');

		await fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Revoke key' }));

		await waitFor(() => expect(revoked).toBe(true));
	});
});
