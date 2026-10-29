import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { makeMaintenancePlan, makeMaintenanceRun } from '../../test/factories';
import { toasts } from '$lib/ui/toast.svelte';
import CatalogMaintenancePlan from './CatalogMaintenancePlan.svelte';

const CATALOG = '00000000-0000-0000-0000-0000000000c1';

describe('CatalogMaintenancePlan', () => {
	it('lists each table, what the next round runs and its last run', async () => {
		let analyzed = false;
		server.use(
			http.get('/catalogs/{catalog_id}/maintenance-plan', ({ response }) =>
				response(200).json([
					{
						namespace: 'sales',
						table_name: 'orders',
						mode: 'managed',
						plan: makeMaintenancePlan(),
						last_run: makeMaintenanceRun({ status: 'gave_up' })
					},
					{
						namespace: 'sales',
						table_name: 'events',
						mode: 'off',
						plan: makeMaintenancePlan({ plan: [] }),
						last_run: null
					}
				])
			),
			http.post('/catalogs/{catalog_id}/table-health/analyze', ({ response }) => {
				analyzed = true;
				return response(202).empty();
			})
		);
		render(CatalogMaintenancePlan, { props: { catalogId: CATALOG } });

		const link = await screen.findByRole('link', { name: 'sales.orders' });
		expect(link.getAttribute('href')).toBe(`/data?catalog=${CATALOG}&ns=sales&table=orders`);
		expect(screen.getByText('Expire snapshots')).toBeTruthy();
		expect(screen.getByText('Gave up')).toBeTruthy();
		expect(screen.getByText('Nothing due')).toBeTruthy();
		expect(screen.getByText('Off')).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Analyze now' }));
		await waitFor(() => expect(analyzed).toBe(true));
		await waitFor(() =>
			expect(toasts.items.map((toast) => toast.message)).toContain(
				'Analysis started; results show up in a few minutes.'
			)
		);
	});

	it('points to Data when no table has parameters', async () => {
		server.use(
			http.get('/catalogs/{catalog_id}/maintenance-plan', ({ response }) => response(200).json([]))
		);
		render(CatalogMaintenancePlan, { props: { catalogId: CATALOG } });
		expect(await screen.findByText('No table has maintenance parameters')).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Go to Data' })).toBeTruthy();
	});
});
