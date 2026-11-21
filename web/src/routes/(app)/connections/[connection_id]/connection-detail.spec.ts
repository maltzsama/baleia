import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../../../test/server';
import {
	makeBindingSupport,
	makeConnection,
	makeEngine,
	makeMe,
	makeProvider
} from '../../../../test/factories';
import { renderPage } from '../../../../test/render';

const CONNECTION = '00000000-0000-0000-0000-0000000000e1';
const ENGINE = '00000000-0000-0000-0000-000000000004';
vi.mock('$app/state', () => ({
	page: {
		url: new URL('http://localhost/connections/00000000-0000-0000-0000-0000000000e1'),
		params: { connection_id: '00000000-0000-0000-0000-0000000000e1' }
	}
}));

import ConnectionDetailPage from './+page.svelte';

const topicsProvider = makeProvider({
	catalog_schema: [{ key: 'topics', label: 'Topics', type: 'text', required: true, secret: false }]
});

function mockApi() {
	server.use(
		http.get('/catalog-connections/{id}', ({ response }) =>
			response(200).json(makeConnection({ id: CONNECTION, name: 'stream' }))
		),
		http.get('/connections/{connection_id}/catalogs', ({ response }) => response(200).json([])),
		http.get('/engines', ({ response }) =>
			response(200).json([makeEngine({ id: ENGINE, class: 'federated', name: 'trino' })])
		),
		http.get('/catalog-connections/{id}/impact', ({ response }) =>
			response(200).json({ catalogs: 0, bindings: 0, running_executions: 0 })
		),
		http.get('/engines/{id}/binding-support', ({ response }) =>
			response(200).json({ testdb: makeBindingSupport() })
		),
		http.get('/audit/events', ({ response }) => response(200).json({ items: [] }))
	);
}

describe('connection detail', () => {
	beforeEach(() => {
		localStorage.clear();
		mockApi();
	});

	it('asks for the catalog configuration the type declares when adding a catalog', async () => {
		const bodies: Record<string, unknown>[] = [];
		server.use(
			http.get('/connection-providers', ({ response }) => response(200).json([topicsProvider])),
			http.post('/connections/{connection_id}/catalogs', async ({ request, response }) => {
				bodies.push((await request.json()) as Record<string, unknown>);
				return response(201).json([]);
			})
		);
		renderPage(ConnectionDetailPage, { me: makeMe(), url: `/connections/${CONNECTION}` });

		await fireEvent.click(await screen.findByRole('button', { name: 'Add catalog' }));
		const add = (await screen.findByRole('button', { name: 'Add' })) as HTMLButtonElement;
		expect(add.disabled).toBe(true);
		await fireEvent.input(screen.getByLabelText('Topics *'), { target: { value: 'orders' } });
		expect(add.disabled).toBe(false);
		await fireEvent.click(add);

		await waitFor(() => expect(bodies).toHaveLength(1));
		expect(bodies[0]).toMatchObject({ config: { topics: 'orders' } });
	});

	it('keeps the page and shows the error when the providers cannot be loaded', async () => {
		server.use(http.get('/connection-providers', () => HttpResponse.error()));
		renderPage(ConnectionDetailPage, { me: makeMe(), url: `/connections/${CONNECTION}` });

		await fireEvent.click(await screen.findByRole('button', { name: 'Add catalog' }));
		expect(await screen.findByRole('alert')).toBeTruthy();
		expect(screen.getByText('stream')).toBeTruthy();
	});
});
