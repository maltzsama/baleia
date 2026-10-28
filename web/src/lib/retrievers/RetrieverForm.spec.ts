import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../test/server';
import { makeWorkspace } from '../../test/factories';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import RetrieverForm from './RetrieverForm.svelte';

const WS = '00000000-0000-0000-0000-000000000001';
const BINDING = '00000000-0000-0000-0000-0000000000b1';

function destination() {
	return {
		binding_id: BINDING,
		engine_catalog_name: 'lake',
		connector: 'iceberg',
		catalog: { id: '00000000-0000-0000-0000-0000000000c1', name: 'lake' },
		engine: { id: '00000000-0000-0000-0000-0000000000e1', name: 'trino', kind: 'trino' }
	};
}

describe('RetrieverForm', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
			http.get('/workspaces/{id}/retrievers/destinations', ({ response }) =>
				response(200).json([destination()])
			),
			http.post('/cron/preview', ({ response }) =>
				response(200).json({
					valid: true,
					next: ['2026-10-18T03:00:00Z'],
					min_interval_ok: true
				})
			)
		);
	});

	it('lists the writable destinations and previews the schedule from the server', async () => {
		render(RetrieverForm, { props: { ondone: vi.fn() } });

		await waitFor(() => expect(screen.getByText(/trino · lake/)).toBeTruthy());

		await fireEvent.click(screen.getByRole('button', { name: 'Every day at 03:00' }));
		await waitFor(() => expect(screen.getByText(/2026/)).toBeTruthy());
	});

	it('shows the minimum-interval warning from the preview', async () => {
		server.use(
			http.post('/cron/preview', ({ response }) =>
				response(200).json({
					valid: true,
					next: ['2026-10-18T03:00:00Z'],
					min_interval_ok: false
				})
			)
		);
		render(RetrieverForm, { props: { ondone: vi.fn() } });

		await waitFor(() => expect(screen.getByText(/The minimum interval is 5 minutes/)).toBeTruthy());
	});
	// #462: a validation result only stands for the SQL and destination it ran on.
	it('drops the validation result when the definition changes', async () => {
		let valid = true;
		server.use(
			http.post('/workspaces/{id}/retrievers/validate', ({ response }) =>
				valid
					? response(200).json({ ok: true })
					: response(422).json({ code: 'validation_error', message: 'bad SQL', errors: [] })
			)
		);
		render(RetrieverForm, { props: { ondone: vi.fn() } });
		await waitFor(() => expect(screen.getByText(/trino · lake/)).toBeTruthy());
		const sql = screen.getByLabelText('Definition (one SELECT) *');

		await fireEvent.click(screen.getByRole('button', { name: 'Validate on the destination' }));
		expect(await screen.findByText('Valid on the destination')).toBeTruthy();
		await fireEvent.input(sql, { target: { value: 'SELECT 2' } });
		expect(screen.queryByText('Valid on the destination')).toBeNull();

		valid = false;
		await fireEvent.click(screen.getByRole('button', { name: 'Validate on the destination' }));
		expect(await screen.findByText('Validation failed')).toBeTruthy();
		await fireEvent.input(sql, { target: { value: 'SELECT 3' } });
		expect(screen.queryByText('Validation failed')).toBeNull();
		expect(screen.queryByText('bad SQL')).toBeNull();
	});

	it('keeps the result when only the schedule changes', async () => {
		server.use(
			http.post('/workspaces/{id}/retrievers/validate', ({ response }) =>
				response(200).json({ ok: true })
			)
		);
		render(RetrieverForm, { props: { ondone: vi.fn() } });
		await waitFor(() => expect(screen.getByText(/trino · lake/)).toBeTruthy());

		await fireEvent.click(screen.getByRole('button', { name: 'Validate on the destination' }));
		expect(await screen.findByText('Valid on the destination')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Every day at 03:00' }));
		expect(screen.getByText('Valid on the destination')).toBeTruthy();
	});
});
