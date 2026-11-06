import { screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../../../test/server';
import { makeEngine, makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';

const ENGINE = '00000000-0000-0000-0000-000000000004';
vi.mock('$app/state', () => ({
	page: {
		url: new URL('http://localhost/engines/00000000-0000-0000-0000-000000000004'),
		params: { engine_id: '00000000-0000-0000-0000-000000000004' }
	}
}));

import EngineDetailPage from './+page.svelte';

describe('engine detail', () => {
	beforeEach(() => {
		localStorage.clear();
	});

	it('renders the engine detail', async () => {
		server.use(
			http.get('/engines/{id}', ({ response }) =>
				response(200).json(makeEngine({ id: ENGINE, class: 'direct' }))
			),
			http.get('/engines/{id}/impact', ({ response }) =>
				response(200).json({
					managed_bindings: 0,
					native_catalogs: 0,
					grants: 0,
					metrics: 0,
					docs: 0,
					saved_queries_targeting: 0,
					running_executions: 0
				})
			),
			http.get('/engines/{id}/bindings', ({ response }) => response(200).json([]))
		);
		renderPage(EngineDetailPage, { me: makeMe(), url: `/engines/${ENGINE}` });

		expect(await screen.findByText('Guarantees')).toBeTruthy();
	});
});
