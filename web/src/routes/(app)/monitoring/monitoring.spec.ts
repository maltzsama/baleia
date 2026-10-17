import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../../test/server';
import { makeMe, makeWorkspace } from '../../../test/factories';
import { authStore } from '$lib/stores/auth.svelte';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import MonitoringPage from './+page.svelte';

const ENGINE = '00000000-0000-0000-0000-0000000000e1';
const WORKSPACE = '00000000-0000-0000-0000-000000000001';

describe('monitoring page', () => {
	beforeEach(() => {
		authStore.set(makeMe());
		workspaceStore.select(makeWorkspace({ id: WORKSPACE }));
		server.use(
			http.get('/workspaces', ({ response }) =>
				response(200).json([makeWorkspace({ id: WORKSPACE })])
			),
			http.get('/monitoring/engines', ({ response }) =>
				response(200).json([{ id: ENGINE, name: 'trino', kind: 'trino', class: 'federated' }])
			),
			http.get('/engines/{id}/monitoring/cluster', ({ response }) =>
				response(200).json({
					latest: {
						captured_at: new Date().toISOString(),
						active_workers: 3,
						running_queries: 2,
						queued_queries: 0,
						blocked_queries: 0,
						total_memory_bytes: 100,
						free_memory_bytes: 40
					},
					series: [
						{
							captured_at: new Date().toISOString(),
							active_workers: 3,
							running_queries: 2,
							queued_queries: 0,
							blocked_queries: 0
						}
					]
				})
			)
		);
	});

	it('renders the cluster tiles and hides the engine selector with one engine', async () => {
		render(MonitoringPage);

		expect(await screen.findByText('Workers')).toBeTruthy();
		expect(screen.getByText('Running')).toBeTruthy();
		await waitFor(() => expect(screen.getByText('3')).toBeTruthy());
		expect(screen.queryByLabelText('Engine')).toBeNull();
	});
});
