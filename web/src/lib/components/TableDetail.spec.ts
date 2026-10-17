import { render, screen } from '@testing-library/svelte';
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
});
