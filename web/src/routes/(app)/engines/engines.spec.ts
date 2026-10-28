import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { authStore } from '$lib/stores/auth.svelte';
import { http, server } from '../../../test/server';
import { makeEngine, makeMe } from '../../../test/factories';
import EnginesPage from './+page.svelte';
import type { EngineCapabilities } from '$lib/engines/client';

function caps(overrides: Partial<EngineCapabilities> = {}): EngineCapabilities {
	return {
		dialect: 'trino',
		identity_modes: ['impersonation', 'connection'],
		read_only: 'enforced',
		supports_cancel: true,
		cancel_scope: 'any_process',
		transactions: true,
		multiple_result_sets: true,
		multiple_catalogs: true,
		namespace_depth: 3,
		session_schema: true,
		supports_explain: true,
		supports_time_travel: true,
		supports_catalog_ddl: true,
		monitoring: true,
		iceberg_maintenance: true,
		arrow_native: true,
		max_identifier_length: 255,
		identifier_case: 'lower',
		...overrides
	};
}

describe('engines list', () => {
	it('shows the three read-only chip variants from capabilities, not kind literals', async () => {
		server.use(
			http.get('/engines', ({ response }) =>
				response(200).json([
					makeEngine({
						id: '00000000-0000-0000-0000-0000000000a1',
						name: 'trino',
						capabilities: caps(),
						health: { status: 'ok', checked_at: '2026-01-01T00:00:00Z', error_code: null }
					}),
					makeEngine({
						id: '00000000-0000-0000-0000-0000000000a2',
						name: 'analytics_pg',
						kind: 'postgresql',
						class: 'direct',
						credentials_read_only: true,
						capabilities: caps({ dialect: 'postgresql', read_only: 'none' }),
						health: {
							status: 'unreachable',
							checked_at: '2026-01-01T00:00:00Z',
							error_code: 'unreachable'
						}
					}),
					makeEngine({
						id: '00000000-0000-0000-0000-0000000000a3',
						name: 'local_sqlite',
						kind: 'sqlite',
						class: 'direct',
						credentials_read_only: false,
						capabilities: caps({ dialect: 'sqlite', read_only: 'none' })
					})
				])
			),
			http.get('/catalog-connections', ({ response }) => response(200).json([]))
		);

		render(EnginesPage);

		await waitFor(() => expect(screen.getAllByText('trino').length).toBeGreaterThan(0));
		expect(screen.getByText('Read-only enforced')).toBeTruthy();
		expect(screen.getByText('Read-only via credentials')).toBeTruthy();
		expect(screen.getByText('Read-only not available')).toBeTruthy();
		expect(screen.queryByText('Register engine')).toBeNull();
		expect(screen.getAllByText('Unreachable').length).toBeGreaterThan(0);
	});

	it('offers registration only when a connection grants register_engine', async () => {
		server.use(
			http.get('/engines', ({ response }) =>
				response(200).json([makeEngine({ capabilities: caps() })])
			),
			http.get('/catalog-connections', ({ response }) =>
				response(200).json([
					{
						id: '00000000-0000-0000-0000-0000000000c1',
						name: 'analytics_pg',
						type: 'postgresql',
						url: '',
						config: {},
						has_credentials: true,
						default_readonly: true,
						credentials_read_only: false,
						created_at: '2026-01-01T00:00:00Z',
						allowed_actions: ['view', 'update', 'test', 'register_engine']
					}
				])
			)
		);

		render(EnginesPage);

		expect(await screen.findByText('Register engine')).toBeTruthy();
	});
	// #476: filters that match nothing can be cleared; no engine at all
	// points to the next step.
	it('clears filters that match nothing', async () => {
		server.use(
			http.get('/engines', ({ response }) =>
				response(200).json([makeEngine({ name: 'main_cluster', capabilities: caps() })])
			),
			http.get('/catalog-connections', ({ response }) => response(200).json([]))
		);
		render(EnginesPage);
		await screen.findByText('main_cluster');

		await fireEvent.change(screen.getByLabelText('Class'), { target: { value: 'direct' } });
		expect(await screen.findByText('No engine matches')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
		expect(await screen.findByText('main_cluster')).toBeTruthy();
	});

	it('offers a new connection when there is no engine and nothing to register from', async () => {
		authStore.set(makeMe());
		server.use(
			http.get('/engines', ({ response }) => response(200).json([])),
			http.get('/catalog-connections', ({ response }) => response(200).json([]))
		);
		render(EnginesPage);

		expect(await screen.findByText('No engines yet')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'New connection' })).toBeTruthy();
	});
});
