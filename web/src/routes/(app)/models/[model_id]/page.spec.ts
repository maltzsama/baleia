import { screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { renderPage } from '../../../../test/render';
import { http, server } from '../../../../test/server';
import {
	makeLineageGraph,
	makeLineageNode,
	makeSemanticModel,
	makeSpace,
	makeWorkspace
} from '../../../../test/factories';

const WS = '00000000-0000-0000-0000-000000000001';
const MODEL = '00000000-0000-0000-0000-0000000000d1';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p }));
vi.mock('$app/state', () => ({
	page: {
		url: new URL('http://localhost/models'),
		params: { model_id: '00000000-0000-0000-0000-0000000000d1' }
	}
}));

import ModelPage from './+page.svelte';

describe('model detail page', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
			http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json([makeSpace()])),
			http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/semantic/models/{model_id}', ({ response }) =>
				response(200).json(makeSemanticModel({ id: MODEL, allowed_actions: ['view'] }))
			),
			http.get('/workspaces/{id}/lineage', ({ request, params, response }) => {
				expect(params.id).toBe(WS);
				expect(new URL(request.url).searchParams.get('node')).toBe(`model:${MODEL}`);
				return response(200).json(
					makeLineageGraph({
						nodes: [
							makeLineageNode({ kind: 'model', id: `model:${MODEL}`, label: 'Orders' }),
							makeLineageNode({
								kind: 'table',
								id: 'table:t',
								label: 'orders',
								detail: 'sales.orders',
								href: undefined
							})
						],
						edges: [{ from: `model:${MODEL}`, to: 'table:t' }]
					})
				);
			})
		);
	});

	it('shows the depends-on lineage section', async () => {
		renderPage(ModelPage);

		expect(await screen.findByText('Depends on')).toBeTruthy();
		expect(await screen.findByText('orders')).toBeTruthy();
	});
});
