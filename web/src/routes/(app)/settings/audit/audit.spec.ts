import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeMe } from '../../../../test/factories';
import { authStore } from '$lib/stores/auth.svelte';
import type { AuditEvent } from '$lib/audit/client';
import AuditPage from './+page.svelte';

const ACTOR = '00000000-0000-0000-0000-000000000003';
const WORKSPACE = '00000000-0000-0000-0000-000000000001';

function event(): AuditEvent {
	return {
		id: 1,
		occurred_at: new Date().toISOString(),
		actor_kind: 'user',
		actor_id: ACTOR,
		auth_method: 'session',
		action: 'connection.update',
		resource_type: 'connection',
		resource_id: '00000000-0000-0000-0000-0000000000c1',
		workspace_id: WORKSPACE,
		outcome: 'allowed',
		reason: null,
		request_id: 'req-1',
		trace_id: 'trace-1',
		client_ip: '127.0.0.1',
		user_agent: 'vitest',
		changes: { name: { from: 'old', to: 'new' } }
	};
}

describe('audit page', () => {
	beforeEach(() => {
		authStore.set(makeMe());
		window.history.replaceState(null, '', '/settings/audit');
		server.use(
			http.get('/audit/actions', ({ response }) => response(200).json(['connection.update'])),
			http.get('/audit/events', ({ response }) =>
				response(200).json({ items: [event()], next_cursor: null })
			),
			http.post('/audit/resolve', ({ response }) =>
				response(200).json({
					users: { [ACTOR]: { name: 'Ada Lovelace', email: 'ada@example.com' } },
					workspaces: { [WORKSPACE]: { name: 'Personal' } }
				})
			)
		);
	});

	it('renders the event and opens the drawer with its changes', async () => {
		render(AuditPage);

		expect(await screen.findByText('Ada Lovelace')).toBeTruthy();
		expect(screen.getByText('Personal')).toBeTruthy();

		await fireEvent.click(screen.getByText('Ada Lovelace'));
		expect(await screen.findByText('Changes')).toBeTruthy();
		expect(screen.getByText('"old"')).toBeTruthy();
		expect(screen.getByText('"new"')).toBeTruthy();
	});

	// #485: each event opens from a named control, not a focusable row.
	it('opens an event from a named button in its row', async () => {
		render(AuditPage);
		await screen.findByText('Ada Lovelace');

		const open = screen.getByRole('button', { name: /^Open event .* by Ada Lovelace$/ });
		expect(open.closest('td')).toBeTruthy();
		expect(open.closest('tr')?.getAttribute('tabindex')).toBeNull();
		await fireEvent.click(open);
		expect(await screen.findByText('Changes')).toBeTruthy();
		expect(screen.getAllByRole('columnheader')[0].getAttribute('scope')).toBe('col');
	});

	it('sends the selected window as from and to filters', async () => {
		let from: string | null = null;
		server.use(
			http.get('/audit/events', ({ request, response }) => {
				from = new URL(request.url).searchParams.get('from');
				return response(200).json({ items: [], next_cursor: null });
			})
		);
		render(AuditPage);
		await waitFor(() => expect(from).not.toBeNull());
		expect(new Date(from!).getTime()).toBeLessThan(Date.now());
	});
});
