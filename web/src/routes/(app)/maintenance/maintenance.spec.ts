import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { clearMetadataCache } from '$lib/metadata/client';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../test/server';
import { setTestUrl } from '../../../test/nav.svelte';
import {
	makeSearchResult,
	makeTableDetail,
	makeTableMaintenance,
	makeWorkspace
} from '../../../test/factories';

// The page reads its table from the URL-backed selection store (#29).
vi.mock('$app/state', async () => ({ page: (await import('../../../test/nav.svelte')).testPage }));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

import MaintenancePage from './+page.svelte';

const WS = '00000000-0000-0000-0000-000000000001';
const CATALOG = '00000000-0000-0000-0000-0000000000c1';
const URL = `/maintenance?catalog=${CATALOG}&ns=sales&table=orders`;

function useBackend({ found = true }: { found?: boolean } = {}) {
	server.use(
		http.get('/workspaces/{id}/metadata/search', ({ response }) =>
			response(200).json(found ? [makeSearchResult()] : [])
		),
		http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/tables', ({ response }) =>
			response(200).json({
				freshness: { metadata_synced_at: null },
				items: [],
				next_cursor: null
			})
		),
		http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) =>
			response(200).json(makeTableDetail())
		),
		http.get('/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance', ({ response }) =>
			response(200).json(makeTableMaintenance({ properties: {} }))
		),
		http.get('/audit/events', ({ response }) => response(200).json({ items: [] }))
	);
}

describe('maintenance page', () => {
	beforeEach(() => {
		clearMetadataCache();
		workspaceStore.select(makeWorkspace({ id: WS }));
	});

	it('renders the maintenance of the table named in the URL', async () => {
		useBackend();
		setTestUrl(URL);
		render(MaintenancePage);

		expect(await screen.findByRole('heading', { level: 1, name: 'Maintenance' })).toBeTruthy();
		expect(await screen.findByText('"lake_t"."sales"."orders"')).toBeTruthy();
		expect(await screen.findByText(/has no maintenance parameters/)).toBeTruthy();
	});

	it('offers the way back to monitoring when the URL names no table', async () => {
		setTestUrl('/maintenance');
		render(MaintenancePage);

		expect(await screen.findByText('Select a table')).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Back to monitoring' }).getAttribute('href')).toBe(
			'/monitoring'
		);
		expect(screen.getByRole('link', { name: 'Monitoring' }).getAttribute('href')).toBe(
			'/monitoring'
		);
	});

	it('reports a table the catalog does not have', async () => {
		useBackend({ found: false });
		setTestUrl(URL);
		render(MaintenancePage);

		expect(await screen.findByText('Table not found')).toBeTruthy();
	});
});
