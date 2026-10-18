import { screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import type { components } from '$lib/api/schema';
import { http, server } from '../../../../test/server';
import { makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import SystemPage from './+page.svelte';

type SystemStatus = components['schemas']['SystemStatus'];

const status: SystemStatus = {
	baleia: {
		status: 'ok',
		latency_ms: 0,
		version: 'dev',
		commit: 'abc123',
		started_at: '2026-01-01T00:00:00Z'
	},
	database: { status: 'unknown', error_code: 'database_unavailable', latency_ms: 1 },
	temporal: { status: 'ok' },
	object_storage: { status: 'ok' },
	smtp: { status: 'ok', configured: true },
	schedules: {
		status: 'ok',
		items: [{ name: 'metadata-sync', status: 'missing', detail: 'schedule_missing' }]
	},
	engines: { status: 'ok', items: [{ name: 'trino', status: 'ok' }] },
	bindings: { status: 'ok', items: [{ name: 'active', status: 'ok', value: 2 }] },
	metadata: { status: 'ok', items: [{ name: 'catalogs', status: 'ok', value: 1 }] },
	reconcile: { status: 'ok', items: [] },
	audit_export: { status: 'ok', items: [{ name: 'configured', status: 'missing', value: 0 }] },
	plugin: {
		status: 'ok',
		items: [{ name: 'expected_version', status: 'ok', detail: '0.9.2' }]
	}
};

describe('system status page', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/system');
		server.use(http.get('/admin/system/status', ({ response }) => response(200).json(status)));
	});

	it('shows every component and the unknown error code', async () => {
		renderPage(SystemPage, { me: makeMe(), url: '/settings/system' });

		expect(await screen.findByText('Baleia')).toBeTruthy();
		expect(screen.getByText('Database')).toBeTruthy();
		expect(screen.getByText('Schedules')).toBeTruthy();
		expect(screen.getByText('Plugin')).toBeTruthy();
		expect(screen.getByText('database_unavailable')).toBeTruthy();
	});

	it('highlights a missing schedule', async () => {
		renderPage(SystemPage, { me: makeMe(), url: '/settings/system' });

		const row = await screen.findByText('metadata-sync');
		expect(row.closest('li')?.textContent).toContain('missing');
	});

	it('offers a manual refresh', async () => {
		renderPage(SystemPage, { me: makeMe(), url: '/settings/system' });
		expect(await screen.findByRole('button', { name: 'Refresh' })).toBeTruthy();
	});
});
