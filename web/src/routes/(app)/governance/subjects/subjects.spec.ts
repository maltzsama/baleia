import { fireEvent, screen, within } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeCatalog, makeDataPolicy, makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import SubjectsPage from './+page.svelte';

vi.mock('$lib/ui/patterns/CodeBlock.svelte', async () => ({
	default: (await import('../../../../test/CodeBlockStub.svelte')).default
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({
	page: { url: new URL('http://localhost/governance/subjects'), params: {} }
}));

const ana = '00000000-0000-0000-0000-0000000000a0';
const bo = '00000000-0000-0000-0000-0000000000b0';
const team = '00000000-0000-0000-0000-0000000000c0';

const person = (id: string, name: string) => ({
	id,
	name,
	email: `${name.toLowerCase()}@example.com`,
	role: 'member' as const,
	auth_provider: 'local' as const,
	kind: 'human' as const,
	status: 'active' as const,
	created_at: '2026-01-01T00:00:00Z'
});

function mock() {
	server.use(
		http.get('/catalogs', ({ response }) =>
			response(200).json([
				{
					...makeCatalog({ name: 'governed', allowed_actions: ['manage_data_policies'] }),
					origin: 'source',
					bindings: [],
					needs_attention: false,
					table_health: false,
					read_only: false
				}
			])
		),
		http.get('/catalogs/{id}/data-policies', ({ response }) =>
			response(200).json({
				enabled: true,
				names: ['governed'],
				draft: false,
				policies: [
					makeDataPolicy({ id: 'f1', name: 'team-eu', subjects: [{ team }] }),
					makeDataPolicy({
						id: 'm1',
						name: 'email-mask',
						kind: 'column_mask',
						column: 'email',
						mode: null,
						subjects: [{ everyone: true, except: [{ user: bo }] }],
						expression: "'***'"
					}),
					makeDataPolicy({ id: 'f2', name: 'orders-bo', table: 'orders', subjects: [{ user: bo }] })
				]
			})
		),
		http.get('/users', ({ response }) =>
			response(200).json({
				data: [person(ana, 'Ana'), person(bo, 'Bo')],
				pagination: { page: 1, per_page: 100, total: 2, total_pages: 1 }
			})
		),
		http.get('/teams', ({ response }) =>
			response(200).json([{ id: team, name: 'EU', created_at: '2026-01-01T00:00:00Z' }])
		),
		http.get('/teams/{id}/members', ({ response }) =>
			response(200).json([
				{ user: person(ana, 'Ana'), role: 'member', joined_at: '2026-01-01T00:00:00Z' }
			])
		),
		http.get('/service-accounts', ({ response }) => response(200).json([])),
		http.get('/engines', ({ response }) => response(200).json([]))
	);
}

async function choose(name: string) {
	await fireEvent.change(await screen.findByRole('combobox', { name: 'Subject' }), {
		target: { value: name }
	});
	await fireEvent.click(screen.getByRole('button', { name: 'Show' }));
}

// #1146: what is filtered and masked for one subject (ADR 0028).
describe('governance by subject', () => {
	it("shows a user's filters through their teams, masks and default-deny tables", async () => {
		mock();
		renderPage(SubjectsPage, { me: makeMe() });
		await choose(ana);

		expect(await screen.findByText('For User Ana')).toBeTruthy();
		const catalog = within(await screen.findByRole('region', { name: 'governed' }));
		const customers = catalog.getByText('sales.customers').closest('tr')!;
		expect(await within(customers).findByText("(region = 'EU')")).toBeTruthy();
		expect(within(customers).getByText("'***'")).toBeTruthy();
		// orders has a row filter that does not name Ana: she reads no rows.
		const orders = catalog.getByText('sales.orders').closest('tr')!;
		expect(within(orders).getByText('No rows')).toBeTruthy();
		expect(screen.getByText(/identity-provider groups are not included/)).toBeTruthy();
	});

	it('shows a failed team lookup instead of treating it as no membership', async () => {
		mock();
		server.use(
			http.get('/teams/{id}/members', ({ response }) =>
				response(404).json({ code: 'not_found', message: 'boom' })
			)
		);
		renderPage(SubjectsPage, { me: makeMe() });
		await choose(ana);
		expect(await screen.findByText("Could not load the subject's teams")).toBeTruthy();
		expect(screen.queryByRole('region', { name: 'governed' })).toBeNull();
	});

	it('says the teams are unknown when they cannot be listed', async () => {
		mock();
		server.use(
			http.get('/teams', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'boom' })
			)
		);
		renderPage(SubjectsPage, { me: makeMe() });
		await choose(ana);
		expect(
			await screen.findByText(/the policies this user gets through a team cannot be shown/)
		).toBeTruthy();
		expect(screen.queryByRole('region', { name: 'governed' })).toBeNull();
	});

	it('honours the exceptions of everyone', async () => {
		mock();
		renderPage(SubjectsPage, { me: makeMe() });
		await choose(bo);

		const catalog = within(await screen.findByRole('region', { name: 'governed' }));
		const customers = catalog.getByText('sales.customers').closest('tr')!;
		expect(within(customers).getByText('No rows')).toBeTruthy();
		expect(within(customers).getByText('None')).toBeTruthy();
	});
});
