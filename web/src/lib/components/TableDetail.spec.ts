import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { clearMetadataCache } from '$lib/metadata/client';
import { http, server } from '../../test/server';
import { makeMetadataBinding, makeTableDetail, makeWorkspace } from '../../test/factories';
import TableDetail from './TableDetail.svelte';
import type { TableRef } from '$lib/metadata/client';

const TABLE_ID = '00000000-0000-0000-0000-0000000000d1';

function tableRef(bindings = [makeMetadataBinding()]): TableRef {
	return {
		tableId: TABLE_ID,
		catalogId: '00000000-0000-0000-0000-0000000000c1',
		catalogName: 'lake',
		namespace: ['sales'],
		tableName: 'orders',
		bindings
	};
}

function mockDetail() {
	server.use(
		http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) =>
			response(200).json(makeTableDetail())
		)
	);
}

describe('TableDetail', () => {
	beforeEach(() => {
		clearMetadataCache();
		workspaceStore.select(makeWorkspace());
	});

	it('renders columns, types, properties and freshness from the snapshot', async () => {
		mockDetail();
		render(TableDetail, { props: { table: tableRef() } });

		expect(await screen.findByText('orders')).toBeTruthy();
		expect(await screen.findByText('id')).toBeTruthy();
		expect(screen.getByText('bigint')).toBeTruthy();
		expect(screen.getByText('varchar')).toBeTruthy();
		expect(screen.getByText('format')).toBeTruthy();
		expect(screen.getByText('iceberg')).toBeTruthy();
	});

	it('hides the engine picker with a single binding', async () => {
		mockDetail();
		render(TableDetail, { props: { table: tableRef() } });
		await screen.findByText('orders');
		expect(screen.queryByLabelText('Engine binding')).toBeNull();
	});

	it('shows the engine picker when the catalog has several bindings', async () => {
		mockDetail();
		const second = makeMetadataBinding({
			binding_id: '00000000-0000-0000-0000-0000000000b2',
			engine_name: 'postgres',
			engine_kind: 'postgresql',
			engine_catalog_name: 'app_db'
		});
		render(TableDetail, {
			props: { table: tableRef([makeMetadataBinding(), second]) }
		});
		expect(await screen.findByLabelText('Engine binding')).toBeTruthy();
	});
	// #456: while the next table or binding loads, nothing of the previous
	// one is shown or actionable, and a failure never leaves it on screen.
	describe('switching', () => {
		const OTHER = '00000000-0000-0000-0000-0000000000d2';
		const invoices: TableRef = {
			...tableRef(),
			tableId: OTHER,
			tableName: 'invoices'
		};

		function gatedDetail() {
			let release: (ok: boolean) => void = () => {};
			const gate = new Promise<boolean>((resolve) => (release = resolve));
			server.use(
				http.get('/workspaces/{id}/metadata/tables/{table_id}', async ({ params, response }) => {
					if (params.table_id === TABLE_ID) return response(200).json(makeTableDetail());
					if (!(await gate)) {
						return response(404).json({ code: 'not_found', message: 'table not found' });
					}
					return response(200).json(
						makeTableDetail({
							id: OTHER,
							name: 'invoices',
							qualified_name: '"lake_t"."sales"."invoices"'
						})
					);
				})
			);
			return release;
		}

		it('hides the previous table while the next one loads', async () => {
			const release = gatedDetail();
			const view = render(TableDetail, { props: { table: tableRef() } });
			expect(await screen.findByText('orders')).toBeTruthy();

			await view.rerender({ table: invoices });
			await waitFor(() => expect(screen.queryByText('orders')).toBeNull());
			expect(screen.queryByRole('button', { name: 'Insert name' })).toBeNull();
			expect(screen.queryByRole('button', { name: 'Preview' })).toBeNull();

			release(true);
			expect(await screen.findByText('invoices')).toBeTruthy();
			expect(
				(screen.getByRole('button', { name: 'Insert name' }) as HTMLButtonElement).disabled
			).toBe(false);
		});

		it('shows the error of the next table, not the previous one', async () => {
			const release = gatedDetail();
			const view = render(TableDetail, { props: { table: tableRef() } });
			expect(await screen.findByText('orders')).toBeTruthy();

			await view.rerender({ table: invoices });
			release(false);
			expect(await screen.findByText('Could not load the table')).toBeTruthy();
			expect(screen.queryByText('orders')).toBeNull();
		});

		it('disables the actions while another binding loads', async () => {
			const second = makeMetadataBinding({
				binding_id: '00000000-0000-0000-0000-0000000000b2',
				engine_name: 'postgres'
			});
			let releaseSecond: () => void = () => {};
			const gate = new Promise<void>((resolve) => (releaseSecond = resolve));
			server.use(
				http.get('/workspaces/{id}/metadata/tables/{table_id}', async ({ request, response }) => {
					const binding = new URL(request.url).searchParams.get('binding_id');
					if (binding === second.binding_id) await gate;
					return response(200).json(makeTableDetail({ binding_id: binding }));
				})
			);
			render(TableDetail, { props: { table: tableRef([makeMetadataBinding(), second]) } });
			const insert = (await screen.findByRole('button', {
				name: 'Insert name'
			})) as HTMLButtonElement;
			expect(insert.disabled).toBe(false);

			await fireEvent.change(screen.getByLabelText('Engine binding'), {
				target: { value: second.binding_id }
			});
			await waitFor(() => expect(insert.disabled).toBe(true));
			releaseSecond();
			await waitFor(() => expect(insert.disabled).toBe(false));
		});
	});
});
