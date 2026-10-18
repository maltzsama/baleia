import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeMe } from '../../../../test/factories';
import { authStore } from '$lib/stores/auth.svelte';
import AuditPage from './+page.svelte';

function handlers() {
	server.use(
		http.get('/audit/actions', ({ response }) => response(200).json([])),
		http.get('/audit/events', ({ response }) =>
			response(200).json({ items: [], next_cursor: null })
		)
	);
}

describe('settings audit scoping', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/audit');
	});

	it('hides the all-events controls from a member', async () => {
		handlers();
		authStore.set(makeMe({ role: 'member', global_actions: [] }));
		render(AuditPage);

		await waitFor(() =>
			expect(screen.getByText(/Your events and the events of workspaces you own/)).toBeTruthy()
		);
		expect(screen.queryByRole('button', { name: 'Export CSV' })).toBeNull();
		expect(screen.queryByLabelText('Actor id')).toBeNull();
	});

	it('shows the all-events controls to an admin', async () => {
		handlers();
		authStore.set(makeMe());
		render(AuditPage);

		await waitFor(() =>
			expect(screen.getByText(/Every security and data operation on this instance/)).toBeTruthy()
		);
		expect(screen.getByRole('button', { name: 'Export CSV' })).toBeTruthy();
		expect(screen.getByLabelText('Actor id')).toBeTruthy();
	});
});
