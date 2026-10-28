import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import ModelEditor from '$lib/semantic/ModelEditor.svelte';
import { http, server } from '../../../test/server';
import {
	makeCatalogBinding,
	makeEngine,
	makeSearchResult,
	makeSemanticModel,
	makeSpace,
	makeTableDetail,
	makeWorkspace
} from '../../../test/factories';

const WS = '00000000-0000-0000-0000-000000000001';

const nav = vi.hoisted(() => ({ goto: vi.fn() }));
vi.mock('$app/navigation', () => ({ goto: nav.goto }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/models'), params: {} } }));

import ModelsPage from './+page.svelte';

function baseHandlers(models = [makeSemanticModel()]) {
	server.use(
		http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
		http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json([makeSpace()])),
		http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/semantic/models', ({ response }) => response(200).json(models)),
		http.get('/engines', ({ response }) => response(200).json([makeEngine()])),
		http.get('/catalogs', ({ response }) => response(200).json([])),
		http.get('/catalogs/{id}/bindings', ({ response }) =>
			response(200).json([makeCatalogBinding()])
		)
	);
}

describe('models list', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		nav.goto.mockReset();
	});

	it('lists models with their source and field counts', async () => {
		baseHandlers([makeSemanticModel({ certified: true })]);

		render(ModelsPage);

		expect(await screen.findByText('Orders')).toBeTruthy();
		expect(screen.getByText('public.orders')).toBeTruthy();
		expect(screen.getByText('3 dimensions · 1 metrics · 0 joins')).toBeTruthy();
		expect(screen.getByText('Certified')).toBeTruthy();
	});

	it('shows the empty state', async () => {
		baseHandlers([]);

		render(ModelsPage);

		expect(await screen.findByText('No models')).toBeTruthy();
	});

	it('shows an error state with retry when the list fails', async () => {
		baseHandlers();
		server.use(
			http.get('/workspaces/{id}/semantic/models', ({ response }) =>
				response(422).json({ code: 'validation_error', message: 'boom', errors: [] })
			)
		);

		render(ModelsPage);

		expect(await screen.findByText('Could not load models')).toBeTruthy();
	});
});

describe('model editor', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		nav.goto.mockReset();
	});

	it('generates dimensions from the picked table and creates the model', async () => {
		baseHandlers([]);
		server.use(
			http.get('/workspaces/{id}/metadata/search', ({ response }) =>
				response(200).json([makeSearchResult()])
			),
			http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) =>
				response(200).json(
					makeTableDetail({
						columns: [
							{
								ordinal: 1,
								name: 'id',
								native_type: 'long',
								type_system: 'iceberg',
								logical_type: 'int64'
							},
							{
								ordinal: 2,
								name: 'created_at',
								native_type: 'timestamp',
								type_system: 'iceberg',
								logical_type: 'timestamp'
							},
							{
								ordinal: 3,
								name: 'payload',
								native_type: 'json',
								type_system: 'iceberg',
								logical_type: 'json'
							}
						]
					})
				)
			)
		);
		let created: Record<string, unknown> | undefined;
		server.use(
			http.post('/workspaces/{id}/semantic/models', async ({ request, response }) => {
				created = (await request.json()) as Record<string, unknown>;
				return response(201).json(makeSemanticModel());
			})
		);

		render(ModelEditor);
		const search = await screen.findByLabelText('Table (search by name)');
		await fireEvent.input(search, { target: { value: 'orders' } });
		await fireEvent.click(await screen.findByRole('button', { name: 'sales.orders' }));

		// id and created_at become dimensions; the json column is skipped.
		await waitFor(() => expect(screen.getAllByLabelText('Name').length).toBe(2));
		expect(screen.getByText('Time grains')).toBeTruthy();
		// The primary key defaults to a column named id, so the form can be saved.
		const save = screen.getByRole('button', { name: 'Save' });
		await waitFor(() => expect((save as HTMLButtonElement).disabled).toBe(false));
		await fireEvent.click(save);

		await waitFor(() => expect(created).toBeDefined());
		const definition = created?.definition as {
			source: unknown;
			primary_key: string[];
			dimensions: { name: string; type: string }[];
		};
		expect(definition.source).toEqual({ kind: 'table', namespace: ['sales'], table: 'orders' });
		expect(definition.primary_key).toEqual(['id']);
		expect(definition.dimensions.map((d) => [d.name, d.type])).toEqual([
			['id', 'int64'],
			['created_at', 'timestamp']
		]);
		expect(created?.label).toBe('orders');
		expect(created?.slug).toBe('orders');
		await waitFor(() => expect(nav.goto).toHaveBeenCalled());
	});

	it('shows the join tree for a model with joins', async () => {
		const customers = makeSemanticModel({
			id: '00000000-0000-0000-0000-0000000000d2',
			slug: 'customers',
			label: 'Customers'
		});
		const orders = makeSemanticModel({
			definition: {
				...(makeSemanticModel().definition as object),
				joins: [
					{
						to: 'customers',
						relationship: 'many_to_one',
						on: [{ from: 'id', to: 'id' }]
					}
				]
			}
		});
		baseHandlers([orders, customers]);

		render(ModelEditor, { model: orders });

		const tree = await screen.findByTestId('join-tree');
		expect(tree.textContent).toContain('many_to_one');
		expect(tree.textContent).toContain('customers');
	});

	it('shows the API message when the save is rejected', async () => {
		baseHandlers([]);
		server.use(
			http.patch('/workspaces/{id}/semantic/models/{model_id}', ({ response }) =>
				response(422).json({
					code: 'invalid_definition',
					message: 'model "orders" joins unknown model "ghost"',
					errors: []
				})
			)
		);
		const model = makeSemanticModel();

		render(ModelEditor, { model });
		const save = await screen.findByRole('button', { name: 'Save' });
		await waitFor(() => expect((save as HTMLButtonElement).disabled).toBe(false));
		await fireEvent.click(save);

		expect(await screen.findByText(/joins unknown model "ghost"/)).toBeTruthy();
		expect(screen.getByText('The query cannot be compiled')).toBeTruthy();
	});

	it('saves custom SQL and filtered metrics with their format', async () => {
		baseHandlers([]);
		let sent: Record<string, unknown> | undefined;
		server.use(
			http.patch('/workspaces/{id}/semantic/models/{model_id}', async ({ request, response }) => {
				sent = (await request.json()) as Record<string, unknown>;
				return response(200).json(makeSemanticModel());
			})
		);

		render(ModelEditor, { model: makeSemanticModel(), onsaved: () => {} });
		const kind = await screen.findByLabelText('Kind');
		await fireEvent.change(kind, { target: { value: 'sql' } });
		await fireEvent.input(screen.getByLabelText(/Aggregate expression/), {
			target: { value: 'SUM(id) - 1' }
		});
		const decimals = screen.getAllByLabelText('Decimals');
		await fireEvent.input(decimals[decimals.length - 1], { target: { value: '2' } });
		await fireEvent.click(screen.getAllByRole('button', { name: 'Add filter' })[0]);
		const operators = screen.getAllByLabelText('Operator');
		await fireEvent.change(operators[operators.length - 1], { target: { value: 'in' } });
		const values = screen.getAllByLabelText('Value');
		await fireEvent.input(values[values.length - 1], { target: { value: '1, 2' } });
		const save = screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement;
		await waitFor(() => expect(save.disabled).toBe(false));
		await fireEvent.click(save);

		await waitFor(() => expect(sent).toBeDefined());
		const metric = (sent?.definition as { metrics: Record<string, unknown>[] }).metrics[0];
		expect(metric).toEqual({
			name: 'revenue',
			label: 'Revenue',
			sql: 'SUM(id) - 1',
			format: { type: 'number', decimals: 2 },
			filters: [{ column: 'id', op: 'in', values: [1, 2] }]
		});
	});

	it('does not send certification when the user may not certify', async () => {
		baseHandlers([]);
		let sent: Record<string, unknown> | undefined;
		server.use(
			http.patch('/workspaces/{id}/semantic/models/{model_id}', async ({ request, response }) => {
				sent = (await request.json()) as Record<string, unknown>;
				return response(200).json(makeSemanticModel());
			})
		);
		const model = makeSemanticModel({ certified: true, allowed_actions: ['view', 'update'] });

		render(ModelEditor, { model, onsaved: () => {} });
		const save = await screen.findByRole('button', { name: 'Save' });
		await waitFor(() => expect((save as HTMLButtonElement).disabled).toBe(false));
		await fireEvent.click(save);

		await waitFor(() => expect(sent).toBeDefined());
		expect(sent).not.toHaveProperty('certified');
		expect(screen.queryByLabelText('Certified')).toBeNull();
	});

	it('previews the SQL of a sample exploration for a saved model', async () => {
		baseHandlers([makeSemanticModel()]);
		server.use(
			http.post('/workspaces/{id}/semantic/explore/compile', ({ response }) =>
				response(200).json({ sql: 'SELECT status FROM orders', notes: [], dialect: 'postgresql' })
			)
		);

		render(ModelEditor, { model: makeSemanticModel() });
		await fireEvent.click(await screen.findByRole('button', { name: 'Compile a sample' }));

		await waitFor(() => expect(document.body.textContent).toContain('SELECT status FROM orders'));
	});
});

describe('v1 migration dialog', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		nav.goto.mockReset();
	});

	it('dry-runs, takes a primary key for a new model and applies', async () => {
		baseHandlers([]);
		const bodies: { dry_run: boolean; tables?: unknown[] }[] = [];
		server.use(
			http.post('/workspaces/{id}/semantic/models/migrate-v1', async ({ request, response }) => {
				const body = (await request.json()) as { dry_run: boolean; tables?: unknown[] };
				bodies.push(body);
				const keyed = (body.tables?.length ?? 0) > 0;
				return response(200).json({
					dry_run: body.dry_run,
					items: [
						{
							catalog_id: '00000000-0000-0000-0000-0000000000e2',
							namespace: ['sales'],
							table: 'orders',
							model_slug: 'orders',
							model_id: body.dry_run ? null : '00000000-0000-0000-0000-0000000000d1',
							metrics: ['revenue'],
							columns: ['order_id', 'amount'],
							primary_key: keyed ? ['order_id'] : [],
							action: keyed ? 'CREATE' : 'ERROR',
							error_code: keyed ? null : 'primary_key_required',
							error: keyed ? null : 'choose the primary key columns of orders'
						}
					]
				});
			})
		);

		render(ModelsPage);
		await fireEvent.click(await screen.findByRole('button', { name: 'Migrate v1 metrics' }));

		expect(await screen.findByText('Choose the primary key columns of this table.')).toBeTruthy();
		expect(screen.getByText('Needs attention')).toBeTruthy();
		const apply = screen.getByRole('button', { name: 'Apply (0)' }) as HTMLButtonElement;
		expect(apply.disabled).toBe(true);

		await fireEvent.click(screen.getByLabelText('order_id'));
		await fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Apply (1)' }));

		expect(await screen.findByText('Migration applied.')).toBeTruthy();
		expect(bodies.map((b) => b.dry_run)).toEqual([true, true, false]);
		expect(bodies[2].tables).toEqual([
			{
				catalog_id: '00000000-0000-0000-0000-0000000000e2',
				namespace: ['sales'],
				table: 'orders',
				primary_key: ['order_id']
			}
		]);
	});
	// #460: a failed lookup is not an empty list; retry keeps the edits.
	it('shows a failed catalog lookup and retries it without losing edits', async () => {
		baseHandlers([]);
		let fail = true;
		server.use(
			http.get('/catalogs', ({ response }) =>
				fail ? HttpResponse.error() : response(200).json([])
			)
		);
		render(ModelEditor, {});

		const label = (await screen.findByLabelText('Label')) as HTMLInputElement;
		await fireEvent.input(label, { target: { value: 'Revenue' } });
		await fireEvent.change(screen.getByLabelText('Source kind'), { target: { value: 'sql' } });
		expect(await screen.findByText('Could not load the catalogs')).toBeTruthy();

		fail = false;
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
		await waitFor(() => expect(screen.queryByText('Could not load the catalogs')).toBeNull());
		expect(label.value).toBe('Revenue');
	});

	it('explains why joins are unavailable when the models lookup fails', async () => {
		baseHandlers([]);
		server.use(http.get('/workspaces/{id}/semantic/models', () => HttpResponse.error()));
		render(ModelEditor, {});

		expect(await screen.findByText('Could not load the models to join')).toBeTruthy();
	});
});
