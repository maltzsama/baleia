import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

function urlOf(input: RequestInfo | URL): string {
	return input instanceof Request ? input.url : String(input);
}

describe('api client', () => {
	beforeEach(() => {
		vi.resetModules();
		localStorage.clear();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('refreshes once on 401 and retries the request', async () => {
		localStorage.setItem('refresh_token', 'refresh-1');
		let workspaceCalls = 0;
		const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
			const url = urlOf(input);
			if (url.includes('/auth/refresh')) {
				return json({
					token: 'access-2',
					refresh_token: 'refresh-2',
					expires_at: '2030-01-01T00:00:00Z'
				});
			}
			if (url.includes('/workspaces')) {
				workspaceCalls += 1;
				if (workspaceCalls === 1) return json({ code: 'unauthorized', message: 'expired' }, 401);
				return json([{ id: 'ws-1' }]);
			}
			return json({}, 404);
		});
		vi.stubGlobal('fetch', fetchMock);

		const { client, setAccessToken } = await import('./client');
		setAccessToken('access-1');
		const { data } = await client.GET('/workspaces');

		expect(data).toEqual([{ id: 'ws-1' }]);
		expect(fetchMock.mock.calls.some(([input]) => urlOf(input).includes('/auth/refresh'))).toBe(
			true
		);
		expect(workspaceCalls).toBe(2);
	});

	it('emits onSessionExpired when the refresh fails', async () => {
		localStorage.setItem('refresh_token', 'refresh-1');
		const onSessionExpired = vi.fn();
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = urlOf(input);
				if (url.includes('/auth/refresh')) return json({}, 401);
				return json({ code: 'unauthorized', message: 'expired' }, 401);
			})
		);

		const { client, configureAuth, setAccessToken } = await import('./client');
		configureAuth({ onSessionExpired });
		setAccessToken('access-1');
		await client.GET('/workspaces');

		expect(onSessionExpired).toHaveBeenCalledOnce();
	});
});
