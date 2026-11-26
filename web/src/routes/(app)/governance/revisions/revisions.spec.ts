import { fireEvent, screen, waitFor, within } from '@testing-library/svelte';
import { HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { authStore } from '$lib/stores/auth.svelte';
import { http, server } from '../../../../test/server';
import { makeDataPolicyRevision, makeDataPolicyStatus, makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import RevisionsPage from './+page.svelte';

const admin = () => makeMe({ global_actions: ['manage_data_policies'] });
const older = makeDataPolicyRevision({
	id: '00000000-0000-0000-0000-0000000000f0',
	number: 1,
	published: false
});
const current = makeDataPolicyRevision({ number: 2, reason: 'reissue' });

function mock() {
	server.use(
		http.get('/data-policies/status', ({ response }) =>
			response(200).json(
				makeDataPolicyStatus({
					published: current,
					engines: [
						{
							engine_id: '00000000-0000-0000-0000-0000000000a1',
							engine_name: 'trino',
							data_policies: true,
							health: 'unhealthy',
							reason: 'lease_expired',
							active_revision: older.id,
							reported_at: '2026-01-01T00:00:00Z'
						}
					]
				})
			)
		),
		http.get('/data-policies/revisions', ({ response }) => response(200).json([current, older]))
	);
}

// #1146: revisions, rollback and each engine's active revision (ADR 0028).
describe('governance revisions', () => {
	it('is for global admins only (FD9)', async () => {
		renderPage(RevisionsPage, { me: makeMe({ global_actions: ['create_connection'] }) });
		expect(await screen.findByText('Revisions are for global admins')).toBeTruthy();
	});

	// /auth/me can arrive after the page mounts: it loads then, not never.
	it('loads once the admin is known', async () => {
		mock();
		renderPage(RevisionsPage, { me: null });
		expect(screen.getByRole('status', { name: 'Loading data policies' })).toBeTruthy();
		authStore.set(admin());
		expect(await screen.findByText('Re-issue (fresh lease)')).toBeTruthy();
	});

	it('shows each engine health and active revision', async () => {
		mock();
		renderPage(RevisionsPage, { me: admin() });
		const engines = within(
			await screen.findByRole('region', { name: 'Active revision per engine' })
		);
		expect(engines.getByText('Unhealthy')).toBeTruthy();
		expect(engines.getByText("The active revision's lease passed: users are denied.")).toBeTruthy();
		expect(engines.getByText('not the published one yet')).toBeTruthy();
		expect(screen.getByText('Re-issue (fresh lease)')).toBeTruthy();
		expect(screen.getByText('rego-digest')).toBeTruthy();
	});

	// FD4: a rollback is confirmed and names what changes.
	it('confirms a rollback and lists a refusal', async () => {
		mock();
		const rolled = vi.fn();
		server.use(
			http.post('/data-policies/revisions/{id}/rollback', ({ params }) => {
				rolled(params.id);
				return HttpResponse.json(
					{
						code: 'data_policy_invalid',
						message: 'refused',
						problems: [{ code: 'catalog_name_conflict', detail: 'governed' }]
					},
					{ status: 422 }
				);
			})
		);
		renderPage(RevisionsPage, { me: admin() });
		await fireEvent.click(await screen.findByRole('button', { name: 'Roll back to revision #1' }));
		const dialog = await screen.findByRole('dialog', { name: 'Roll back to revision #1' });
		expect(within(dialog).getByText(/Subjects are taken as they are today/)).toBeTruthy();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Roll back' }));

		await waitFor(() => expect(rolled).toHaveBeenCalledWith(older.id));
		const refused = await screen.findByRole('region', { name: 'The rollback was refused' });
		expect(within(refused).getByText(/engine catalog name governed/)).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Roll back to revision #2' })).toBeNull();
	});
});
