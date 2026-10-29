import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import {
	makeMaintenancePlan,
	makeMaintenanceRun,
	makeStoredMaintenancePolicy,
	makeTableMaintenance
} from '../../test/factories';
import TableMaintenance from './TableMaintenance.svelte';
import type { MaintenancePolicy } from './client';

const CATALOG = '00000000-0000-0000-0000-0000000000c1';
const props = { catalogId: CATALOG, namespace: 'sales', table: 'orders' };
const PATH = '/catalogs/{catalog_id}/tables/{namespace}/{table}/maintenance';

describe('TableMaintenance', () => {
	it('starts new parameters from the Iceberg table properties and creates them', async () => {
		let sent: { body: MaintenancePolicy; ifMatch: string | null } | null = null;
		server.use(
			http.get(PATH, ({ response }) =>
				response(200).json(
					makeTableMaintenance({
						properties: {
							'history.expire.max-snapshot-age-ms': '432000000',
							'history.expire.min-snapshots-to-keep': '5'
						}
					})
				)
			),
			http.put(PATH, async ({ request, response }) => {
				sent = {
					body: (await request.json()) as MaintenancePolicy,
					ifMatch: request.headers.get('If-Match')
				};
				return response(200).json(makeStoredMaintenancePolicy());
			})
		);
		render(TableMaintenance, { props });

		expect(await screen.findByText(/has no maintenance parameters/)).toBeTruthy();
		const expire = screen.getByRole('group', { name: 'Expire snapshots' });
		await fireEvent.click(within(expire).getByLabelText('Enabled'));
		expect((within(expire).getByLabelText('Retention (hours)') as HTMLInputElement).value).toBe(
			'120'
		);
		expect((within(expire).getByLabelText('Snapshots to keep') as HTMLInputElement).value).toBe(
			'5'
		);
		expect(
			within(expire).getByText('Table property history.expire.min-snapshots-to-keep = 5')
		).toBeTruthy();
		await fireEvent.input(within(expire).getByLabelText('Snapshots'), {
			target: { value: '100' }
		});

		await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
		await waitFor(() => expect(sent).not.toBeNull());
		expect(sent!.ifMatch).toBeNull();
		expect(sent!.body.expire_snapshots).toMatchObject({
			enabled: true,
			trigger: { snapshots: 100 },
			params: { retention_seconds: 432000, retain_last: 5 }
		});
		expect(sent!.body.optimize.enabled).toBe(false);
	});

	it('replaces parameters at their revision and shows field errors on their inputs', async () => {
		let ifMatch: string | null = null;
		server.use(
			http.get(PATH, ({ response }) =>
				response(200).json(
					makeTableMaintenance({
						policy: makeStoredMaintenancePolicy({ revision: 3 }),
						plan: makeMaintenancePlan()
					})
				)
			),
			http.put(PATH, ({ request, response }) => {
				ifMatch = request.headers.get('If-Match');
				return response(422).json({
					code: 'validation_error',
					message: 'invalid',
					errors: [],
					fields: [
						{
							field: 'expire_snapshots.params.retention_seconds',
							code: 'out_of_range',
							params: { min: '3600' }
						}
					]
				});
			})
		);
		render(TableMaintenance, { props });

		expect(await screen.findByText('Revision 3')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
		expect(await screen.findByText('Fix the highlighted fields.')).toBeTruthy();
		expect(ifMatch).toBe('3');
		const expire = screen.getByRole('group', { name: 'Expire snapshots' });
		expect(within(expire).getByText('This value is out of range. Minimum: 1.')).toBeTruthy();
	});

	it('refuses non-numeric input before sending anything', async () => {
		server.use(
			http.get(PATH, ({ response }) =>
				response(200).json(makeTableMaintenance({ policy: makeStoredMaintenancePolicy() }))
			)
		);
		render(TableMaintenance, { props });
		const expire = await screen.findByRole('group', { name: 'Expire snapshots' });
		await fireEvent.input(within(expire).getByLabelText('Snapshots'), {
			target: { value: 'lots' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
		expect(within(expire).getByText('This value has an invalid format.')).toBeTruthy();
	});

	it('shows the current plan, a dry run and starts a run', async () => {
		let requested = '';
		server.use(
			http.get(PATH, ({ response }) =>
				response(200).json(
					makeTableMaintenance({
						policy: makeStoredMaintenancePolicy(),
						plan: makeMaintenancePlan(),
						runs: [makeMaintenanceRun({ operations: ['expire_snapshots'], trigger: 'automatic' })]
					})
				)
			),
			http.post(`${PATH}/dry-run`, ({ response }) => response(200).json(makeMaintenancePlan())),
			http.post(`${PATH}/runs`, async ({ request, response }) => {
				requested = ((await request.json()) as { operation: string }).operation;
				return response(202).json(
					makeMaintenanceRun({ operations: ['expire_snapshots'], status: 'running' })
				);
			})
		);
		render(TableMaintenance, { props });

		expect(await screen.findAllByText('runs next: snapshots 140 ≥ 100')).toHaveLength(1);
		expect(screen.getByText('Automatic')).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Dry run' }));
		const dialog = await screen.findByRole('dialog');
		expect(within(dialog).getByText('runs next: snapshots 140 ≥ 100')).toBeTruthy();
		await fireEvent.keyDown(dialog, { key: 'Escape' });

		await fireEvent.click(screen.getByRole('button', { name: 'Run now' }));
		await waitFor(() => expect(requested).toBe('next_due'));
	});

	it('removes the parameters after confirmation', async () => {
		let removed = false;
		server.use(
			http.get(PATH, ({ response }) =>
				response(200).json(
					makeTableMaintenance({ policy: removed ? null : makeStoredMaintenancePolicy() })
				)
			),
			http.delete(PATH, ({ response }) => {
				removed = true;
				return response(204).empty();
			})
		);
		render(TableMaintenance, { props });

		await fireEvent.click(await screen.findByRole('button', { name: 'Remove parameters' }));
		const dialog = await screen.findByRole('dialog');
		expect(within(dialog).getByText(/its history stays/)).toBeTruthy();
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Remove parameters' }));
		expect(await screen.findByText(/has no maintenance parameters/)).toBeTruthy();
		expect(removed).toBe(true);
	});

	it('hides the operations of a table maintained elsewhere', async () => {
		server.use(
			http.get(PATH, ({ response }) =>
				response(200).json(
					makeTableMaintenance({
						policy: makeStoredMaintenancePolicy({ mode: 'maintained_elsewhere' })
					})
				)
			)
		);
		render(TableMaintenance, { props });
		expect(await screen.findByText(/Another system maintains this table/)).toBeTruthy();
		expect(screen.queryByRole('group', { name: 'Optimize' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Run now' })).toBeNull();
	});

	it('is read-only without the update action', async () => {
		server.use(
			http.get(PATH, ({ response }) =>
				response(200).json(
					makeTableMaintenance({
						allowed_actions: ['view'],
						policy: makeStoredMaintenancePolicy(),
						plan: makeMaintenancePlan()
					})
				)
			)
		);
		render(TableMaintenance, { props });
		expect(await screen.findByText(/but not change it/)).toBeTruthy();
		expect((screen.getByLabelText('Mode') as HTMLSelectElement).matches(':disabled')).toBe(true);
		expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'Run now' })).toBeNull();
	});

	it('shows the error state when loading fails', async () => {
		server.use(
			http.get(PATH, ({ response }) =>
				response(404).json({ code: 'not_found', message: 'catalog not found' })
			)
		);
		render(TableMaintenance, { props });
		expect(await screen.findByText('Could not load maintenance')).toBeTruthy();
	});
});
