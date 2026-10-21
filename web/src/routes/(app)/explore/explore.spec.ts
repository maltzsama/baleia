import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { encodeShare, type ExploreQuery } from '$lib/semantic/explore';
import { http, server } from '../../../test/server';
import {
	makeCatalogBinding,
	makeEngine,
	makeSemanticModel,
	makeWorkspace
} from '../../../test/factories';

const WS = '00000000-0000-0000-0000-000000000001';

const nav = vi.hoisted(() => ({ url: new URL('http://localhost/explore'), goto: vi.fn() }));
vi.mock('$app/state', () => ({
	page: {
		get url() {
			return nav.url;
		},
		params: {}
	}
}));
vi.mock('$app/navigation', () => ({ goto: nav.goto }));

import ExplorePage from './+page.svelte';

function useBackend(compile: (body: { query: ExploreQuery }) => Promise<Response> | Response) {
	server.use(
		http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
		http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/semantic/models', ({ response }) =>
			response(200).json([makeSemanticModel()])
		),
		http.get('/engines', ({ response }) => response(200).json([makeEngine()])),
		http.get('/catalogs/{id}/bindings', ({ response }) =>
			response(200).json([makeCatalogBinding()])
		),
		http.post('/workspaces/{id}/semantic/explore/compile', async ({ request }) =>
			compile((await request.json()) as { query: ExploreQuery })
		)
	);
}

const ok = (sql: string, notes: string[] = []) =>
	Response.json({ sql, notes, dialect: 'postgresql' });

describe('explore page', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		nav.goto.mockReset();
		nav.url = new URL('http://localhost/explore');
	});

	it('shows the field tree of the picked model and ignores hidden fields', async () => {
		useBackend(() => ok('SELECT 1'));
		nav.url = new URL(
			`http://localhost/explore?q=${encodeShare({ model: 'orders', metrics: ['revenue'] })}`
		);

		render(ExplorePage);

		expect(await screen.findByText('Status')).toBeTruthy();
		expect(screen.getByText('Created at')).toBeTruthy();
		expect(screen.getAllByText('Revenue').length).toBeGreaterThan(0);
	});

	it('compiles the shared query and shows the SQL with the compiler notes', async () => {
		const bodies: ExploreQuery[] = [];
		useBackend((body) => {
			bodies.push(body.query);
			return ok('SELECT SUM(id) FROM orders', ['Pre-aggregated orders to avoid fan-out']);
		});
		nav.url = new URL(
			`http://localhost/explore?q=${encodeShare({ model: 'orders', metrics: ['revenue'] })}`
		);

		render(ExplorePage);
		await fireEvent.click(await screen.findByRole('tab', { name: 'SQL' }));

		expect(await screen.findByText(/Pre-aggregated orders/)).toBeTruthy();
		expect(bodies[0]).toEqual({ model: 'orders', metrics: ['revenue'] });
	});

	it('serializes the selection into ?q= and never sends SQL to the compiler', async () => {
		const bodies: unknown[] = [];
		useBackend((body) => {
			bodies.push(body);
			return ok('SELECT 1');
		});
		nav.url = new URL(
			`http://localhost/explore?q=${encodeShare({ model: 'orders', metrics: ['revenue'] })}`
		);

		render(ExplorePage);
		await fireEvent.click(await screen.findByRole('button', { name: /Status/ }));

		await waitFor(() => expect(nav.goto).toHaveBeenCalled());
		const url = String(nav.goto.mock.calls.at(-1)?.[0]);
		const q = new URL(url, 'http://localhost').searchParams.get('q');
		expect(q).toBe(encodeShare({ model: 'orders', dimensions: ['status'], metrics: ['revenue'] }));
		await waitFor(() => expect(bodies.length).toBeGreaterThan(1));
		expect(JSON.stringify(bodies)).not.toMatch(/"sql"/);
	});

	it('highlights the field a compiler error names', async () => {
		useBackend(() =>
			Response.json(
				{
					code: 'invalid_definition',
					message: 'invalid explore query: unknown dimension "status"',
					errors: []
				},
				{ status: 422 }
			)
		);
		nav.url = new URL(
			`http://localhost/explore?q=${encodeShare({ model: 'orders', dimensions: ['status'] })}`
		);

		render(ExplorePage);

		const alert = await screen.findByRole('alert');
		expect(alert.textContent).toContain('cannot be compiled');
		const chip = document.querySelector('li[data-invalid]');
		expect(chip?.textContent).toContain('Status');
	});

	it('selects a time dimension at a grain from the field tree', async () => {
		useBackend(() => ok('SELECT 1'));
		nav.url = new URL(
			`http://localhost/explore?q=${encodeShare({ model: 'orders', metrics: ['revenue'] })}`
		);

		render(ExplorePage);
		await fireEvent.click(
			await screen.findByRole('button', { name: 'Show the time grains of Created at' })
		);
		await fireEvent.click(screen.getByRole('button', { name: 'month' }));

		await waitFor(() => expect(nav.goto).toHaveBeenCalled());
		const url = String(nav.goto.mock.calls.at(-1)?.[0]);
		const q = new URL(url, 'http://localhost').searchParams.get('q');
		expect(q).toBe(
			encodeShare({
				model: 'orders',
				dimensions: ['created_at'],
				metrics: ['revenue'],
				time_grain: 'month'
			})
		);
	});

	it('names a query the compiler cannot express as not supported', async () => {
		useBackend(() =>
			Response.json(
				{
					code: 'not_supported',
					message: 'explore query not supported yet: chained join to "regions"',
					errors: []
				},
				{ status: 422 }
			)
		);
		nav.url = new URL(
			`http://localhost/explore?q=${encodeShare({ model: 'orders', metrics: ['revenue'] })}`
		);

		render(ExplorePage);

		const alert = await screen.findByRole('alert');
		expect(alert.textContent).not.toContain('cannot be compiled');
		expect(screen.getByText(/chained join to "regions"/)).toBeTruthy();
	});

	it('explains a failed run with the error catalog and the engine detail', async () => {
		useBackend(() => ok('SELECT 1'));
		const EXEC = '00000000-0000-0000-0000-0000000000e9';
		server.use(
			http.post('/workspaces/{id}/executions', ({ response }) =>
				response(202).json({
					id: EXEC,
					workspace_id: WS,
					status: 'running',
					sql: 'SELECT 1',
					read_only: true
				})
			),
			http.get('/workspaces/{id}/executions/{execution_id}', ({ response }) =>
				response(200).json({
					id: EXEC,
					workspace_id: WS,
					status: 'failed',
					sql: 'SELECT 1',
					read_only: true
				})
			),
			http.get('/workspaces/{id}/executions/{execution_id}/statements', ({ response }) =>
				response(200).json([
					{
						id: '00000000-0000-0000-0000-0000000000f9',
						execution_id: EXEC,
						position: 1,
						sql: 'SELECT 1',
						status: 'failed',
						error_code: 'permission_denied',
						error_message: 'Access Denied: Cannot select from table orders'
					}
				])
			)
		);
		nav.url = new URL(
			`http://localhost/explore?q=${encodeShare({ model: 'orders', metrics: ['revenue'] })}`
		);

		render(ExplorePage);
		const run = await screen.findByRole('button', { name: 'Run' });
		await waitFor(() => expect((run as HTMLButtonElement).disabled).toBe(false));
		await fireEvent.click(run);

		const alert = await screen.findByRole('alert');
		expect(alert.textContent).not.toContain('Access Denied');
		await fireEvent.click(screen.getByRole('button', { name: 'Details' }));
		expect(alert.textContent).toContain('Access Denied: Cannot select from table orders');
	});

	it('explains a link to a model the user cannot see', async () => {
		useBackend(() => ok('SELECT 1'));
		nav.url = new URL(`http://localhost/explore?q=${encodeShare({ model: 'ghost' })}`);

		render(ExplorePage);

		expect(await screen.findByText(/"ghost"/)).toBeTruthy();
	});

	it('offers an empty state when the workspace has no models', async () => {
		server.use(
			http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/semantic/models', ({ response }) => response(200).json([]))
		);

		render(ExplorePage);

		expect(await screen.findByText('No models yet')).toBeTruthy();
	});
});
