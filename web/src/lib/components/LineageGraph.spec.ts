import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import LineageGraph from './LineageGraph.svelte';
import { http, server } from '../../test/server';
import {
	makeLineageGraph,
	makeLineageNode,
	makeMetadataCatalog,
	makeTableDetail
} from '../../test/factories';
import { clearMetadataCache, type TableRef } from '$lib/metadata/client';

const WS = '00000000-0000-0000-0000-000000000001';
const TABLE_ID = '00000000-0000-0000-0000-0000000000d1';
const CATALOG_ID = '00000000-0000-0000-0000-0000000000c1';

describe('LineageGraph', () => {
	beforeEach(() => {
		server.resetHandlers();
		clearMetadataCache();
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

	it('links a table that carries an href to the data explorer', async () => {
		server.use(
			http.get('/workspaces/{id}/lineage', ({ response }) =>
				response(200).json(
					makeLineageGraph({
						nodes: [
							makeLineageNode({
								kind: 'table',
								id: `table:${TABLE_ID}`,
								label: 'orders',
								detail: 'sales.orders',
								href: `/data?catalog=${CATALOG_ID}&ns=sales&table=orders`
							})
						],
						edges: []
					})
				)
			)
		);

		render(LineageGraph, { props: { workspaceId: WS, node: 'model:m', direction: 'down' } });

		const link = await screen.findByRole('link', { name: /orders/ });
		expect(link.getAttribute('href')).toContain(`catalog=${CATALOG_ID}`);
	});

	it('opens a table in place when the caller can inspect it', async () => {
		server.use(
			http.get('/workspaces/{id}/lineage', ({ response }) =>
				response(200).json(
					makeLineageGraph({
						nodes: [
							makeLineageNode({
								kind: 'table',
								id: `table:${TABLE_ID}`,
								label: 'orders',
								detail: 'sales.orders',
								href: `/data?catalog=${CATALOG_ID}&ns=sales&table=orders`
							})
						],
						edges: []
					})
				)
			),
			http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) =>
				response(200).json(makeTableDetail())
			),
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) =>
				response(200).json([makeMetadataCatalog()])
			)
		);

		const picked: TableRef[] = [];
		render(LineageGraph, {
			props: {
				workspaceId: WS,
				node: 'saved_query:q',
				direction: 'down',
				onselecttable: (table: TableRef) => picked.push(table)
			}
		});

		await fireEvent.click(await screen.findByRole('button', { name: /orders/ }));

		await waitFor(() => expect(picked).toHaveLength(1));
		expect(picked[0]).toMatchObject({
			tableId: TABLE_ID,
			catalogId: CATALOG_ID,
			catalogName: 'lake',
			namespace: ['sales'],
			tableName: 'orders'
		});
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
