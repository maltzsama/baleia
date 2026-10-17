import { describe, expect, it } from 'vitest';
import { client } from '$lib/api/client';
import { makeWorkspace } from './factories';
import { http, server } from './server';

describe('test harness', () => {
	it('serves a typed mock through the API client', async () => {
		server.use(http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()])));
		const { data } = await client.GET('/workspaces');
		expect(data).toHaveLength(1);
		expect(data?.[0]?.name).toBe('Personal');
	});

	it('fails an unmocked request', async () => {
		await expect(client.GET('/engines')).rejects.toThrow();
	});

	it('rejects a mock whose response does not match the schema', () => {
		const handler = http.get('/workspaces', ({ response }) =>
			// @ts-expect-error -- a Workspace requires id, name, type, created_at…
			response(200).json([{ nope: true }])
		);
		expect(handler).toBeDefined();
	});
});
