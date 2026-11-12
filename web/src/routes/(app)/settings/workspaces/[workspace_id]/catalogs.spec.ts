import { fireEvent, screen, within } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import { http, server } from '../../../../../test/server';
import { makeCatalog, makeMe, makeWorkspace } from '../../../../../test/factories';
import { renderPage } from '../../../../../test/render';
import WorkspacePage from './+page.svelte';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({
	page: {
		url: new URL(
			'http://localhost/settings/workspaces/00000000-0000-0000-0000-000000000001?tab=catalogs'
		),
		params: { workspace_id: '00000000-0000-0000-0000-000000000001' }
	}
}));

describe('workspace catalog administration (#985)', () => {
	it('reuses the searchable catalog flow and refreshes the administrative list', async () => {
		const workspace = makeWorkspace();
		const catalog = makeCatalog({ allowed_actions: ['view', 'grant'] });
		let active = false;
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([workspace])),
			http.get('/workspaces/{id}/targets', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/impact', ({ response }) =>
				response(200).json({
					saved_queries: 0,
					metrics: 0,
					spaces: 0,
					running_executions: 0,
					default_target_affected: false
				})
			),
			http.get('/workspaces/{id}/catalog-access', ({ response }) =>
				response(200).json(
					active
						? [
								{
									catalog_id: catalog.id!,
									catalog_name: catalog.name,
									granted_at: '2026-01-01T00:00:00Z'
								}
							]
						: []
				)
			),
			http.get('/catalog-grants', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/requestable-catalogs', ({ response }) =>
				response(200).json([
					{ id: 'request-only', name: 'Team lake', connection_name: 'Team connection' }
				])
			),
			http.get('/catalogs', ({ response }) =>
				response(200).json([
					{
						...catalog,
						origin: 'native',
						bindings: [],
						needs_attention: false
					}
				])
			),
			http.post('/catalog-grants', ({ response }) => {
				active = true;
				return response(200).json({
					workspace_id: workspace.id,
					catalog_id: catalog.id!,
					state: 'active'
				});
			})
		);
		renderPage(WorkspacePage, {
			me: makeMe(),
			url: `/settings/workspaces/${workspace.id}?tab=catalogs`
		});
		await fireEvent.click(await screen.findByRole('button', { name: 'Workspace catalogs' }));
		const dialog = within(screen.getByRole('dialog', { name: 'Workspace catalogs' }));
		expect(await dialog.findByText('analytics')).toBeTruthy();
		expect(dialog.getByLabelText('Search catalogs or connections')).toBeTruthy();
		expect(dialog.getByRole('button', { name: 'Request access' })).toBeTruthy();
		await fireEvent.click(dialog.getByRole('button', { name: 'Add catalog' }));
		await dialog.findByText('Available in workspace');
		await fireEvent.click(dialog.getByRole('button', { name: 'Close' }));
		expect(await screen.findByText('analytics')).toBeTruthy();
	});
});
