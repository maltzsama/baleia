import { HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { loadDirectory } from './directory';

describe('loadDirectory', () => {
	// One list failing outright (a network error) leaves the others, so the
	// policies still load with what can be named.
	it('keeps the lists that loaded when another request fails', async () => {
		server.use(
			http.get('/users', ({ response }) =>
				response(200).json({
					data: [],
					pagination: { page: 1, per_page: 100, total: 0, total_pages: 1 }
				})
			),
			http.get('/teams', () => HttpResponse.error()),
			http.get('/service-accounts', ({ response }) =>
				response(200).json([
					{
						id: '00000000-0000-0000-0000-0000000000f1',
						name: 'etl',
						email: 'etl@service.local',
						status: 'active',
						allowed_actions: [],
						created_at: '2026-01-01T00:00:00Z'
					}
				])
			)
		);
		const directory = await loadDirectory();
		expect(directory.teams).toEqual([]);
		expect(directory.serviceAccounts.map((s) => s.name)).toEqual(['etl']);
	});
});
