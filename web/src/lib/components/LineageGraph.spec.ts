import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import LineageGraph from './LineageGraph.svelte';
import { http, server } from '../../test/server';
import { makeLineageGraph, makeLineageNode } from '../../test/factories';

const WS = '00000000-0000-0000-0000-000000000001';

describe('LineageGraph', () => {
	beforeEach(() => {
		server.resetHandlers();
	});

	it('lists what uses a table (direction up) as keyboard-navigable links', async () => {
		server.use(
			http.get('/workspaces/{id}/lineage', ({ response }) =>
				response(200).json(
					makeLineageGraph({
						nodes: [
							makeLineageNode({ kind: 'table', id: 'table:t', label: 'orders', href: undefined }),
							makeLineageNode({
								kind: 'model',
								id: 'model:m',
								label: 'Orders model',
								href: '/models/m'
							})
						],
						edges: [{ from: 'model:m', to: 'table:t' }]
					})
				)
			)
		);

		render(LineageGraph, { props: { workspaceId: WS, node: 'table:t', direction: 'up' } });

		expect(await screen.findByText('Used by')).toBeTruthy();
		const link = await screen.findByRole('link', { name: /Orders model/ });
		expect(link).toBeTruthy();
	});

	it('lists what a model depends on (direction down)', async () => {
		server.use(
			http.get('/workspaces/{id}/lineage', ({ response }) =>
				response(200).json(
					makeLineageGraph({
						nodes: [
							makeLineageNode({ kind: 'model', id: 'model:m', label: 'Orders model' }),
							makeLineageNode({
								kind: 'table',
								id: 'table:t',
								label: 'orders',
								detail: 'sales.orders',
								href: undefined
							})
						],
						edges: [{ from: 'model:m', to: 'table:t' }]
					})
				)
			)
		);

		render(LineageGraph, { props: { workspaceId: WS, node: 'model:m', direction: 'down' } });

		expect(await screen.findByText('Depends on')).toBeTruthy();
		expect(await screen.findByText('orders')).toBeTruthy();
	});

	it('shows the empty state when there are no neighbours', async () => {
		server.use(
			http.get('/workspaces/{id}/lineage', ({ response }) =>
				response(200).json(
					makeLineageGraph({
						nodes: [
							makeLineageNode({ kind: 'table', id: 'table:t', label: 'orders', href: undefined })
						],
						edges: []
					})
				)
			)
		);

		render(LineageGraph, { props: { workspaceId: WS, node: 'table:t', direction: 'up' } });

		expect(await screen.findByText('Nothing here yet.')).toBeTruthy();
	});
});
