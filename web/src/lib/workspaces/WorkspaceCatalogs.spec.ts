import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import { tick } from 'svelte';
import type { components } from '$lib/api/schema';
import { catalogAccessStore } from '$lib/stores/catalogAccess.svelte';
import { http, server } from '../../test/server';
import { makeCatalog, makeWorkspace } from '../../test/factories';
import WorkspaceCatalogs from './WorkspaceCatalogs.svelte';

type Grant = components['schemas']['CatalogGrantItem'];
type Summary = components['schemas']['CatalogSummary'];
const workspace = makeWorkspace();
const catalog = {
	...makeCatalog({ allowed_actions: ['view', 'grant'] }),
	origin: 'native',
	provider_kind: 'postgresql',
	bindings: [],
	needs_attention: false,
	connection: { id: 'connection', name: 'Production' }
} satisfies Summary;
const requested = { id: 'requested', name: 'Shared lake', connection_name: 'Team connection' };

function mocks(grants: Grant[] = [], catalogs: Summary[] = [catalog]) {
	server.use(
		http.get('/catalogs', ({ response }) => response(200).json(catalogs)),
		http.get('/catalog-grants', ({ response }) => response(200).json(grants)),
		http.get('/workspaces/{id}/requestable-catalogs', ({ response }) =>
			response(200).json([requested])
		)
	);
}

function row(name: string) {
	return within(screen.getByText(name).closest('li')!);
}

describe('WorkspaceCatalogs (#985)', () => {
	it('shows provider icons and origin, searches, and adds without leaving the flow', async () => {
		mocks();
		const onchange = vi.fn();
		const version = catalogAccessStore.version;
		let body: unknown;
		server.use(
			http.post('/catalog-grants', async ({ request, response }) => {
				body = await request.json();
				return response(200).json({
					workspace_id: workspace.id,
					catalog_id: catalog.id!,
					state: 'active'
				});
			})
		);
		render(WorkspaceCatalogs, { workspace, onchange });
		await screen.findByText('analytics');
		expect(row('analytics').getByText('Production')).toBeTruthy();
		expect(
			screen.getByText('analytics').closest('li')!.querySelector('img')?.getAttribute('src')
		).toContain('postgresql');
		// The requestable endpoint has no provider kind: retain a generic icon and a visible name.
		expect(screen.getByText('Shared lake').closest('li')!.querySelector('svg')).toBeTruthy();
		await fireEvent.input(screen.getByLabelText('Search catalogs or connections'), {
			target: { value: 'Production' }
		});
		expect(screen.queryByText('Shared lake')).toBeNull();
		await fireEvent.click(row('analytics').getByRole('button', { name: 'Add catalog' }));
		expect(await screen.findByText('Available in workspace')).toBeTruthy();
		expect(body).toEqual({ workspace_id: workspace.id, catalog_id: catalog.id });
		expect(catalogAccessStore.version).toBe(version + 1);
		expect(onchange).toHaveBeenCalledOnce();
		expect(row('analytics').queryByRole('button')).toBeNull();
	});

	it('uses the returned pending state and prevents duplicate requests', async () => {
		mocks();
		const version = catalogAccessStore.version;
		server.use(
			http.post('/catalog-grants', ({ response }) =>
				response(200).json({
					workspace_id: workspace.id,
					catalog_id: requested.id,
					state: 'pending',
					initiated_by_side: 'workspace'
				})
			)
		);
		render(WorkspaceCatalogs, { workspace });
		await screen.findByText('Shared lake');
		await fireEvent.click(row('Shared lake').getByRole('button', { name: 'Request access' }));
		expect(await screen.findByText('Awaiting catalog approval')).toBeTruthy();
		expect(row('Shared lake').queryByRole('button')).toBeNull();
		expect(catalogAccessStore.version).toBe(version);
	});

	it('distinguishes outgoing requests from offers and approves only incoming offers', async () => {
		mocks([
			{
				workspace_id: workspace.id,
				catalog_id: catalog.id!,
				state: 'pending',
				initiated_by_side: 'workspace',
				allowed_actions: ['approve']
			},
			{
				workspace_id: workspace.id,
				catalog_id: requested.id,
				state: 'pending',
				initiated_by_side: 'catalog',
				allowed_actions: ['approve']
			}
		]);
		server.use(
			http.post('/catalog-grants/{workspace_id}/{catalog_id}/approve', ({ params, response }) => {
				expect(params.workspace_id).toBe(workspace.id);
				expect(params.catalog_id).toBe(requested.id);
				return response(200).json({
					workspace_id: workspace.id,
					catalog_id: requested.id,
					state: 'active'
				});
			})
		);
		render(WorkspaceCatalogs, { workspace });
		await screen.findByText('Awaiting your approval');
		expect(row('analytics').queryByRole('button')).toBeNull();
		await fireEvent.click(row('Shared lake').getByRole('button', { name: 'Approve' }));
		expect(await screen.findByText('Available in workspace')).toBeTruthy();
	});

	it('shows existing active access and an empty search result', async () => {
		mocks([{ workspace_id: workspace.id, catalog_id: catalog.id!, state: 'active' }]);
		render(WorkspaceCatalogs, { workspace });
		await screen.findByText('Available in workspace');
		expect(row('analytics').queryByRole('button')).toBeNull();
		await fireEvent.input(screen.getByLabelText('Search catalogs or connections'), {
			target: { value: 'missing' }
		});
		expect(await screen.findByText('No catalogs found')).toBeTruthy();
	});

	it('fetches every page of grantable catalogs', async () => {
		mocks([], []);
		const offsets: number[] = [];
		server.use(
			http.get('/catalogs', ({ request, response }) => {
				const offset = Number(new URL(request.url).searchParams.get('offset'));
				offsets.push(offset);
				return response(200).json(
					offset === 0
						? Array.from({ length: 200 }, (_, index) => ({
								...catalog,
								id: `catalog-${index}`,
								name: `Catalog ${index}`
							}))
						: [{ ...catalog, id: 'last', name: 'Last catalog' }]
				);
			})
		);
		render(WorkspaceCatalogs, { workspace });
		expect(await screen.findByText('Last catalog')).toBeTruthy();
		expect(offsets).toEqual([0, 200]);
	});

	it('shows loading, reports a load failure, and allows retry', async () => {
		mocks();
		server.use(
			http.get('/workspaces/{id}/requestable-catalogs', ({ response }) =>
				response(404).json({ code: 'not_found', message: 'Workspace not found' })
			)
		);
		render(WorkspaceCatalogs, { workspace });
		expect(screen.getByRole('status', { name: 'Loading catalogs' })).toBeTruthy();
		expect(await screen.findByRole('alert')).toBeTruthy();
		mocks();
		await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
		expect(await screen.findByText('analytics')).toBeTruthy();
	});

	it('keeps a failed mutation actionable and does not publish an access change', async () => {
		mocks();
		const version = catalogAccessStore.version;
		server.use(
			http.post('/catalog-grants', ({ response }) =>
				response(403).json({ code: 'forbidden', message: 'Access denied' })
			)
		);
		render(WorkspaceCatalogs, { workspace });
		await screen.findByText('analytics');
		await fireEvent.click(row('analytics').getByRole('button', { name: 'Add catalog' }));
		await screen.findByRole('alert');
		await waitFor(() =>
			expect(
				row('analytics').getByRole('button', { name: 'Add catalog' }).hasAttribute('disabled')
			).toBe(false)
		);
		expect(catalogAccessStore.version).toBe(version);
	});

	it('does not fetch management data or offer mutations without permission', async () => {
		mocks();
		const onRequest = vi.fn();
		server.events.on('request:start', onRequest);
		try {
			render(WorkspaceCatalogs, { workspace: makeWorkspace({ allowed_actions: ['view'] }) });
			await tick();
			// Give fetch interception a turn after the component's effects have run.
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(onRequest).not.toHaveBeenCalled();
			expect(
				screen.getByText('Ask a workspace administrator to add a catalog or request access.')
			).toBeTruthy();
			expect(screen.queryByRole('button')).toBeNull();
		} finally {
			server.events.removeListener('request:start', onRequest);
		}
	});
});
