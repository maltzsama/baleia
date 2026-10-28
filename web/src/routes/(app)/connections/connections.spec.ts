import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import {
	makeBindingSupport,
	makeCatalog,
	makeCatalogBinding,
	makeConnection,
	makeEngine,
	makeProvider
} from '../../../test/factories';
import { http, server } from '../../../test/server';
import { authStore } from '$lib/stores/auth.svelte';
import { makeMe } from '../../../test/factories';
import ConnectionsPage from './+page.svelte';

const CONNECTION = '00000000-0000-0000-0000-0000000000e1';
const CATALOG = '00000000-0000-0000-0000-0000000000e2';
const ENGINE = '00000000-0000-0000-0000-000000000004';
const OTHER_CONNECTION = '00000000-0000-0000-0000-000000000009';

function mockApi() {
	server.use(
		http.get('/catalog-connections', ({ response }) =>
			response(200).json([makeConnection({ id: CONNECTION, name: 'Warehouse' })])
		),
		http.get('/connection-providers', ({ response }) => response(200).json([makeProvider()])),
		http.get('/engines', ({ response }) =>
			response(200).json([
				makeEngine({
					id: ENGINE,
					name: 'warehouse',
					kind: 'testengine',
					connection_id: OTHER_CONNECTION
				})
			])
		),
		http.get('/engines/{id}/binding-support', ({ response }) =>
			response(200).json({ testdb: makeBindingSupport() })
		),
		http.get('/connections/{connection_id}/catalogs', ({ response }) =>
			response(200).json([makeCatalog({ id: CATALOG, connection_id: CONNECTION, name: 'lake' })])
		),
		http.get('/catalogs/{id}/bindings', ({ response }) =>
			response(200).json([makeCatalogBinding({ id: 'b1', catalog_id: CATALOG })])
		),
		http.delete('/catalog-connections/{id}', ({ response }) => response(204).empty()),
		http.post('/engines', ({ response }) =>
			response(201).json(
				makeEngine({
					id: '00000000-0000-0000-0000-0000000000e9',
					name: 'warehouse-engine',
					kind: 'testengine',
					connection_id: CONNECTION
				})
			)
		)
	);
}

describe('connections page', () => {
	beforeEach(() => {
		mockApi();
	});

	it('lists the connection with its catalog, bindings and provider label', async () => {
		render(ConnectionsPage);

		expect(await screen.findByText('Warehouse')).toBeTruthy();
		expect(screen.getByText(/Test Database/)).toBeTruthy();
		expect(screen.getByText('lake')).toBeTruthy();
		expect(screen.getByText(/1 binding/)).toBeTruthy();
	});

	it('confirms deletion with the resource name and removes the card', async () => {
		render(ConnectionsPage);
		await screen.findByText('Warehouse');

		await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
		await fireEvent.input(screen.getByLabelText('Type "Warehouse" to confirm'), {
			target: { value: 'Warehouse' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Remove connection' }));

		await waitFor(() => expect(screen.queryByText('Warehouse')).toBeNull());
	});

	it('registers a direct engine from the connection card', async () => {
		render(ConnectionsPage);
		await screen.findByText('Warehouse');

		await fireEvent.click(screen.getByRole('button', { name: 'Register engine' }));

		expect(await screen.findByText('warehouse-engine')).toBeTruthy();
	});
	// #476: no connections at all is onboarding; a search with no match is not.
	it('offers to create the first connection when there is none', async () => {
		authStore.set(makeMe());
		server.use(http.get('/catalog-connections', ({ response }) => response(200).json([])));
		render(ConnectionsPage);

		expect(await screen.findByText('No connections yet')).toBeTruthy();
		// The page header has one too; the empty state adds the onboarding one.
		expect(screen.getAllByRole('button', { name: 'New connection' })).toHaveLength(2);
	});

	it('says the search matched nothing and lets it be cleared', async () => {
		render(ConnectionsPage);
		await screen.findByText('Warehouse');
		await fireEvent.input(screen.getByPlaceholderText('Search connections…'), {
			target: { value: 'nothing' }
		});

		expect(await screen.findByText('No connection matches “nothing”')).toBeTruthy();
		expect(screen.queryByText('No connections yet')).toBeNull();
		await fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
		expect(await screen.findByText('Warehouse')).toBeTruthy();
	});
});
