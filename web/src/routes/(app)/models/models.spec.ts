import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import ModelEditor from '$lib/semantic/ModelEditor.svelte';
import { http, server } from '../../../test/server';
import {
	makeCatalogBinding,
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
