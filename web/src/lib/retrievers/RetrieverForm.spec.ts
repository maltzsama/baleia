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
});
