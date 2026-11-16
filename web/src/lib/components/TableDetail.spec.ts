import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { clearMetadataCache } from '$lib/metadata/client';
import { http, server } from '../../test/server';
import {
	makeBiKey,
	makeMetadataBinding,
	makeTableDetail,
	makeWorkspace,
	makeWorkspaceBIStatus
} from '../../test/factories';
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

	it('explains an unsupported table and offers no preview or name (#977)', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) =>
				response(200).json(
					makeTableDetail({
						columns: [],
						qualified_name: null,
						preview_sql: null,
						unsupported: { code: 'table_kind', detail: 'TABLE_DB_STORAGE' }
					})
				)
			)
		);
		render(TableDetail, { props: { table: tableRef() } });

		const note = await screen.findByRole('note');
		expect(note.textContent).toContain('Unsupported table');
		expect(note.textContent).toContain('type: TABLE_DB_STORAGE');
		expect(note.textContent).toContain('cannot be read');
		expect((screen.getByRole('button', { name: 'Preview' }) as HTMLButtonElement).disabled).toBe(
			true
		);
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

// #1067: the actions download a connection file through the guided BI dialog.
// They appear for a Trino target; the dialog explains any missing gateway,
// workspace or key prerequisite instead of silently hiding the actions.
describe('BI connection file', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ bi_enabled: true }));
	});

	it('offers both downloads for a Trino target and opens the guided dialog', async () => {
		mockDetail();
		server.use(
			http.get('/workspaces/{id}/bi-status', ({ response }) =>
				response(200).json(makeWorkspaceBIStatus())
			),
			http.get('/workspaces/{id}/bi-keys', ({ response }) => response(200).json([makeBiKey()]))
		);
		render(TableDetail, { props: { table: tableRef() } });
		await screen.findByText('orders');

		expect(screen.getByRole('button', { name: 'Download Tableau connection' })).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Download Power BI connection' }));
		expect(
			await screen.findByRole('dialog', { name: 'Download the Power BI connection' })
		).toBeTruthy();
	});

	it('is not offered for a table the source cannot read (#977)', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) =>
				response(200).json(
					makeTableDetail({
						columns: [],
						qualified_name: null,
						preview_sql: null,
						unsupported: { code: 'refused' }
					})
				)
			)
		);
		// Its own table id: the detail of the shared one is cached by the tests around it.
		render(TableDetail, {
			props: { table: { ...tableRef(), tableId: '00000000-0000-0000-0000-0000000000d9' } }
		});
		await screen.findByRole('note');
		expect(screen.queryByRole('button', { name: 'Download Tableau connection' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Download Power BI connection' })).toBeNull();
	});

	it('is not offered for a non-Trino target', async () => {
		mockDetail();
		const postgres = makeMetadataBinding({
			engine_kind: 'postgresql',
			engine_catalog_name: 'app_db'
		});
		render(TableDetail, { props: { table: tableRef([postgres]) } });
		await screen.findByText('orders');
		expect(screen.queryByRole('button', { name: 'Download Tableau connection' })).toBeNull();
	});
});
