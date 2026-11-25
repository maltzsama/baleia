import { fireEvent, screen, waitFor, within } from '@testing-library/svelte';
import { HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../../test/server';
import { makeCatalog, makeDataPolicy, makeDataPolicyStatus, makeMe } from '../../../test/factories';
import { renderPage } from '../../../test/render';
import GovernancePage from './+page.svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({
	page: { url: new URL('http://localhost/governance'), params: {} }
}));

const catalogId = '00000000-0000-0000-0000-0000000000e2';
const ada = '00000000-0000-0000-0000-000000000003';

type CatalogAction = NonNullable<Parameters<typeof makeCatalog>[0]>['allowed_actions'];

const summary = (actions: CatalogAction) => ({
	...makeCatalog({ id: catalogId, name: 'governed', allowed_actions: actions }),
	origin: 'source',
	bindings: [],
	needs_attention: false,
	table_health: false,
	read_only: false
});

const filter = makeDataPolicy({ subjects: [{ user: ada }] });
const mask = makeDataPolicy({
	id: '00000000-0000-0000-0000-0000000000d2',
	name: 'email-mask',
	kind: 'column_mask',
	column: 'email',
	mode: null,
	priority: 5,
	subjects: [{ everyone: true }],
	expression: "'***'",
	template: 'constant',
	validation: 'invalid',
	invalid_reason: 'type_mismatch',
	invalid_detail: 'varchar(3)',
	writes: { insert: 'refused', update: 'refused', delete: 'refused', merge: 'refused' }
});

function directory() {
	return [
		http.get('/users', ({ response }) =>
			response(200).json({
				data: [
					{
						id: ada,
						name: 'Ada Lovelace',
						email: 'ada@example.com',
						role: 'member',
						auth_provider: 'local',
						kind: 'human',
						status: 'active',
						created_at: '2026-01-01T00:00:00Z'
					}
				],
				pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 }
			})
		),
		http.get('/teams', ({ response }) => response(200).json([])),
		http.get('/service-accounts', ({ response }) => response(200).json([]))
	];
}

function mockCatalog(
	options: { actions?: CatalogAction; draft?: boolean; enabled?: boolean } = {}
) {
	server.use(
		...directory(),
		http.get('/catalogs', ({ response }) =>
			response(200).json([summary(options.actions ?? ['view', 'manage_data_policies'])])
		),
		http.get('/catalogs/{id}/data-policies', ({ response }) =>
			response(200).json({
				enabled: options.enabled ?? true,
				names: ['governed'],
				draft: options.draft ?? true,
				policies: options.enabled === false ? [] : [filter, mask]
			})
		),
		http.get('/catalogs/{id}/data-policies/definer-views', ({ response }) =>
			response(200).json([
				{
					view: 'governed.sales.eu_customers',
					owner: 'u_owner',
					reads: ['governed.sales.customers'],
					trusted: false,
					unreadable: false
				}
			])
		),
		http.get('/data-policies/status', ({ response }) => response(200).json(makeDataPolicyStatus()))
	);
}

const admin = () => makeMe({ global_actions: ['create_connection', 'manage_data_policies'] });

// #1146: the By object screen (ADR 0028).
describe('governance by object', () => {
	beforeEach(() => {
		document.body.innerHTML = '';
	});

	it('lists a table policies with the guarantee, writes, DEFINER views and engines', async () => {
		mockCatalog();
		renderPage(GovernancePage, { me: admin() });

		const table = await screen.findByRole('region', { name: 'Policies on sales.customers' });
		const rows = within(table);
		expect(rows.getByText('eu-only')).toBeTruthy();
		expect(rows.getByText('User Ada Lovelace')).toBeTruthy();
		expect(rows.getByText('Everyone')).toBeTruthy();
		// FD7: the invalid reason is a localized message, the detail is the construct.
		expect(rows.getByText("The mask's type is not the column's type.")).toBeTruthy();
		expect(rows.getByText('varchar(3)')).toBeTruthy();
		// A mask refuses every write once it applies.
		expect(rows.getAllByText('refused')).toHaveLength(4);
		expect(await rows.findByText('governed.sales.eu_customers')).toBeTruthy();
		expect(rows.getByText('applies the policies of u_owner')).toBeTruthy();

		expect(screen.getByText('Policies hold on the Trino path only')).toBeTruthy();
		expect(screen.getByText(/Close the side doors/)).toBeTruthy();
		expect(screen.getByText('Unpublished changes')).toBeTruthy();
		expect(screen.getByRole('region', { name: 'Active revision per engine' })).toBeTruthy();
		expect(screen.getByText('Where subject attributes come from')).toBeTruthy();
	});

	// FD9: without manage_data_policies on a catalog it is not listed at all.
	it('lists no catalog the caller cannot govern', async () => {
		mockCatalog({ actions: ['view'] });
		renderPage(GovernancePage, { me: makeMe({ global_actions: ['create_connection'] }) });
		expect(await screen.findByText('No catalog to govern')).toBeTruthy();
	});

	it('hides edit and delete without the policy allowed actions', async () => {
		server.use(
			...directory(),
			http.get('/catalogs', ({ response }) =>
				response(200).json([summary(['view', 'manage_data_policies'])])
			),
			http.get('/catalogs/{id}/data-policies', ({ response }) =>
				response(200).json({
					enabled: true,
					names: [],
					draft: false,
					policies: [{ ...filter, allowed_actions: [] }]
				})
			),
			http.get('/catalogs/{id}/data-policies/definer-views', ({ response }) =>
				response(200).json([])
			)
		);
		renderPage(GovernancePage, { me: makeMe() });
		expect(await screen.findByText('eu-only')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'Edit eu-only' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Delete eu-only' })).toBeNull();
	});

	it('says when data policies are off', async () => {
		mockCatalog({ enabled: false });
		renderPage(GovernancePage, { me: admin() });
		expect(await screen.findByText('Data policies are off')).toBeTruthy();
	});

	it('shows a retryable, catalog-backed error', async () => {
		server.use(
			...directory(),
			http.get('/catalogs', ({ response }) =>
				response(200).json([summary(['manage_data_policies'])])
			),
			http.get('/catalogs/{id}/data-policies', () =>
				HttpResponse.json({ code: 'internal', message: 'pq: boom' }, { status: 500 })
			)
		);
		renderPage(GovernancePage, { me: makeMe() });
		expect(await screen.findByText('Could not load data policies')).toBeTruthy();
		expect(screen.queryByText(/pq: boom/)).toBeNull();
		expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
	});

	// FD4: deleting asks first, and says it applies until the next publish.
	it('confirms a delete with its consequences', async () => {
		mockCatalog();
		const deleted = vi.fn();
		server.use(
			http.delete('/data-policies/{id}', ({ params, response }) => {
				deleted(params.id);
				return response(204).empty();
			})
		);
		renderPage(GovernancePage, { me: admin() });
		await fireEvent.click(await screen.findByRole('button', { name: 'Delete eu-only' }));
		const dialog = await screen.findByRole('dialog', { name: 'Delete eu-only' });
		expect(within(dialog).getByText(/stays in force on the engines/)).toBeTruthy();
		expect(deleted).not.toHaveBeenCalled();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
		await waitFor(() => expect(deleted).toHaveBeenCalledWith(filter.id));
	});

	it('lists every reason a publish is refused', async () => {
		mockCatalog();
		server.use(
			http.post('/catalogs/{id}/data-policies/publish', () =>
				HttpResponse.json(
					{
						code: 'data_policy_invalid',
						message: 'refused',
						problems: [
							{ code: 'invalid', policy_id: mask.id, detail: 'type_mismatch' },
							{ code: 'trusted_definer_view', detail: 'governed.sales.all_customers' }
						]
					},
					{ status: 422 }
				)
			)
		);
		renderPage(GovernancePage, { me: admin() });
		await fireEvent.click(await screen.findByRole('button', { name: 'Publish' }));
		const dialog = await screen.findByRole('dialog', { name: 'Publish the policies of governed' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Publish' }));

		const refused = await screen.findByRole('region', { name: 'The publish was refused' });
		expect(
			within(refused).getByText("email-mask: The mask's type is not the column's type.")
		).toBeTruthy();
		expect(
			within(refused).getByText(/governed\.sales\.all_customers is owned by a trusted/)
		).toBeTruthy();
	});

	it('adds a mask from a template, with the hash warning', async () => {
		mockCatalog();
		const created = vi.fn();
		server.use(
			http.post('/catalogs/{id}/data-policies', async ({ request, response }) => {
				const body = await request.json();
				created(body);
				return response(201).json(makeDataPolicy({ ...body, column: body.column ?? null }));
			})
		);
		renderPage(GovernancePage, { me: admin() });
		await fireEvent.click(
			await screen.findByRole('button', { name: 'Add policy to sales.customers' })
		);
		const drawer = await screen.findByRole('dialog', { name: 'New data policy' });
		const form = within(drawer);
		await fireEvent.input(form.getByLabelText('Name'), { target: { value: 'email-hash' } });
		await fireEvent.change(form.getByLabelText('Kind'), { target: { value: 'column_mask' } });
		await fireEvent.input(form.getByLabelText('Column'), { target: { value: 'email' } });
		await fireEvent.change(form.getByLabelText('Subject kind'), { target: { value: 'everyone' } });
		await fireEvent.click(form.getByRole('button', { name: 'Add subject' }));
		await fireEvent.change(form.getByRole('combobox', { name: 'Template' }), {
			target: { value: 'sha256' }
		});
		expect(form.getByText(/unsalted hash of low-cardinality values/)).toBeTruthy();
		await fireEvent.click(form.getByRole('button', { name: 'Fill in the expression' }));
		expect((form.getByLabelText('Expression') as HTMLTextAreaElement).value).toBe(
			'to_hex(sha256(to_utf8(email)))'
		);
		await fireEvent.click(form.getByRole('button', { name: 'Save' }));

		await waitFor(() => expect(created).toHaveBeenCalled());
		expect(created.mock.calls[0][0]).toMatchObject({
			name: 'email-hash',
			kind: 'column_mask',
			schema: 'sales',
			table: 'customers',
			column: 'email',
			subjects: [{ everyone: true }],
			expression: 'to_hex(sha256(to_utf8(email)))',
			template: 'sha256'
		});
	});
});
