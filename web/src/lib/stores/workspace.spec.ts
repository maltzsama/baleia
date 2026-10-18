import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { makeWorkspace } from '../../test/factories';
import { workspaceStore } from './workspace.svelte';

describe('workspaceStore', () => {
	beforeEach(() => {
		localStorage.clear();
	});

	it('deduplicates concurrent load() calls into one request', async () => {
		let calls = 0;
		server.use(
			http.get('/workspaces', ({ response }) => {
				calls += 1;
				return response(200).json([makeWorkspace()]);
			})
		);

		await Promise.all([workspaceStore.load(), workspaceStore.load(), workspaceStore.load()]);

		expect(calls).toBe(1);
	});
});
